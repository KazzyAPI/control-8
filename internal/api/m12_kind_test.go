//go:build k8s

package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/KazzyAPI/control-8/internal/digest"
	"github.com/KazzyAPI/control-8/internal/inventory"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

// M12 acceptance on kind (K8S-051): an unpinned managed_rollout stack follows registry.k8s.io/pause:3.9; the
// "registry" answers with the real digest of pause:3.10 (a tag cannot be pushed to a public registry, D-85), so
// the poll sees a new digest, system requests the image update, and the pod runs the new image. Flipping the
// target to prod then only reports. Needs the network for registry.k8s.io lookups.
func TestKindM12_DigestPoll(t *testing.T) {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		t.Skip("KUBECONFIG not set (run scripts/kind-up.sh)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatal(err)
	}
	dyn, _ := dynamic.NewForConfig(cfg)
	newer, err := crane.Digest("registry.k8s.io/pause:3.10")
	if err != nil {
		t.Skip("registry.k8s.io unreachable: ", err)
	}
	m := newM10(t)
	drv := k8s.NewDriver(dyn)
	m.s.drivers = func(string) (rt.Driver, error) { return drv, nil }
	m.s.now, m.s.tick = time.Now, realTick
	m.eng = inventory.New(m.s.db, time.Second)
	WithInventory(m.eng)(m.s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := drv.Watch(ctx, rt.WatchOptions{Resync: time.Minute, Poll: 5 * time.Second, Enqueue: m.eng.Enqueue})
	if err != nil {
		t.Fatal(err)
	}
	m.eng.SetCache("home", c)
	t.Cleanup(func() {
		dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).Delete(context.Background(), "a", metav1.DeleteOptions{})
	})
	p := &digest.Poller{DB: m.s.db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Resolve: func(context.Context, string) (string, error) { return newer, nil }}
	WithDigest(p)(m.s)
	m.revisionWith(strings.Replace(web3, "image: busybox", "image: registry.k8s.io/pause:3.9", 1), "managed_rollout")
	m.s.db.Exec(`UPDATE revision SET digest_poll=1`)
	if d := m.deployRev(1, "managed_rollout"); d["status"] != "succeeded" {
		t.Fatal(d)
	}
	waitFor(t, "observed image id", func() bool {
		var id *string
		m.s.db.QueryRow(`SELECT image_id FROM service WHERE stack_id=? LIMIT 1`, m.stackID).Scan(&id)
		return id != nil && *id != ""
	})
	p.Poll(ctx)
	m.s.wg.Wait()
	var by, status string
	m.s.db.QueryRow(`SELECT requested_by,status FROM deployment WHERE trigger='digest_poll'`).Scan(&by, &status)
	if by != "system" || status != "succeeded" || m.stack()["desired_revision"] != float64(2) {
		t.Fatal(by, status, m.stack())
	}
	m.s.db.Exec(`UPDATE target SET environment_tier='prod'`)
	m.s.db.Exec(`UPDATE revision SET digest_poll=1`)
	p.Poll(ctx) // desired is now pinned to the "new" digest, so nothing is available: still no new deployment
	if m.count2(`SELECT count(*) FROM deployment WHERE trigger='digest_poll'`) != 1 {
		t.Fatal("prod must not auto-apply")
	}
}

func (m *m10) count2(q string) (n int) { m.s.db.QueryRow(q).Scan(&n); return }
