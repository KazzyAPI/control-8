package policy

import (
	"fmt"
	"slices"
	"testing"

	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

func base() Rules {
	return Rules{ProtectedDBApproval: true, AllowedSafetyProfiles: []string{"managed_rollout", "as_is", "parachute"}}
}

const deploy = `apiVersion: apps/v1
kind: Deployment
metadata: {name: web, labels: {team: x}}
spec:
  template:
    spec:
      %s
      containers:
        - {name: web, image: %s, %s}
`

func dep(extra, image, cext string) string { return fmt.Sprintf(deploy, extra, image, cext) }

// Table test per rule: POL-R1..R4, R6, R7, R9, R12..R14.
func TestPolicyRules(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*Rules, *Input)
		yaml  string
		want  string // violation rule, "" = allowed
		appr  bool
		wantN string
	}{
		{"ok", nil, dep("", "ghcr.io/acme/web:1", "a: b"), "", false, ""},
		{"R1 registry", func(r *Rules, _ *Input) { r.AllowedRegistries = []string{"ghcr.io/acme/"} }, dep("", "evil.io/web:1", "a: b"), "registry_not_allowed", false, ""},
		{"R1 dockerhub default", func(r *Rules, _ *Input) { r.AllowedRegistries = []string{"docker.io/library/"} }, dep("", "nginx:1", "a: b"), "", false, ""},
		{"R2 label", func(r *Rules, _ *Input) { r.RequiredLabels = []string{"owner"} }, dep("", "a/b:1", "a: b"), "label_missing", false, ""},
		{"R3 profile", func(r *Rules, _ *Input) { r.AllowedSafetyProfiles = []string{"as_is"} }, dep("", "a/b:1", "a: b"), "safety_profile_not_allowed", false, ""},
		{"R4 hostPath", nil, dep("volumes: [{name: v, hostPath: {path: /etc}}]", "a/b:1", "a: b"), "host_path_not_allowed", false, ""},
		{"R4 hostPath allowed", func(r *Rules, _ *Input) { r.AllowedBindPrefixes = []string{"/data"} }, dep("volumes: [{name: v, hostPath: {path: /data/x}}]", "a/b:1", "a: b"), "", false, ""},
		{"R4 traversal", func(r *Rules, _ *Input) { r.AllowedBindPrefixes = []string{"/data"} }, dep("volumes: [{name: v, hostPath: {path: /data/../etc}}]", "a/b:1", "a: b"), "host_path_not_allowed", false, ""},
		{"R6 label", nil, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, labels: {control-8/stack: x}}\n", "label_forbidden", false, ""},
		{"R6 annotation", nil, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, annotations: {argocd.argoproj.io/tracking-id: x}}\n", "label_forbidden", false, ""},
		{"R7 privileged", nil, dep("", "a/b:1", "securityContext: {privileged: true}"), "privileged_not_allowed", false, ""},
		{"R7 hostNetwork", nil, dep("hostNetwork: true", "a/b:1", "a: b"), "privileged_not_allowed", false, ""},
		{"R7 cap", nil, dep("", "a/b:1", "securityContext: {capabilities: {add: [NET_ADMIN]}}"), "privileged_not_allowed", false, ""},
		{"R7 hostPort", nil, dep("", "a/b:1", "ports: [{containerPort: 1, hostPort: 80}]"), "privileged_not_allowed", false, ""},
		{"R7 allowed", func(r *Rules, _ *Input) { r.AllowPrivileged = true }, dep("", "a/b:1", "securityContext: {privileged: true}"), "", false, ""},
		{"R9 first apply", func(_ *Rules, i *Input) { i.DatabaseServices, i.DBChanged = []string{"Deployment/web"}, true }, dep("", "postgres:16", "a: b"), "", true, ""},
		{"R9 unchanged", func(_ *Rules, i *Input) { i.DatabaseServices = []string{"Deployment/web"} }, dep("", "postgres:16", "a: b"), "", false, ""},
		{"R9 off", func(r *Rules, i *Input) {
			r.ProtectedDBApproval = false
			i.DatabaseServices, i.DBChanged = []string{"x"}, true
		}, dep("", "postgres:16", "a: b"), "", false, ""},
		{"R12 kind", nil, "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata: {name: a}\n", "kind_not_allowed", false, ""},
		{"R12 namespace kind", nil, "apiVersion: v1\nkind: Namespace\nmetadata: {name: a}\n", "kind_not_allowed", false, ""},
		{"R13 kube-system", func(_ *Rules, i *Input) { i.K8sNamespace = "kube-system" }, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n", "namespace_not_allowed", false, ""},
		{"R13 default", func(_ *Rules, i *Input) { i.K8sNamespace = "default" }, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n", "namespace_not_allowed", false, ""},
		{"R13 allow-list", func(r *Rules, i *Input) { r.AllowedK8sNamespaces = []string{"team-*"}; i.K8sNamespace = "shop" }, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n", "namespace_not_allowed", false, ""},
		{"R13 glob ok", func(r *Rules, i *Input) { r.AllowedK8sNamespaces = []string{"sh*"} }, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n", "", false, ""},
		{"K8S-022 manifest ns", nil, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, namespace: other}\n", "namespace_not_allowed", false, ""},
		{"R14 secret", nil, "apiVersion: v1\nkind: Secret\nmetadata: {name: a}\n", "kind_not_allowed", false, ""},
	}
	for _, c := range cases {
		r, in := base(), Input{K8sNamespace: "shop", Profile: "managed_rollout"}
		if c.edit != nil {
			c.edit(&r, &in)
		}
		objs, err := k8s.Parse(c.yaml)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		in.Objects = objs
		res := Evaluate(r, in)
		got := ""
		if len(res.Violations) > 0 {
			got = res.Violations[0].Rule
		}
		if got != c.want || res.Allowed != (c.want == "") || res.RequiresApproval != c.appr {
			t.Errorf("%s: %+v", c.name, res)
		}
	}
}

// 05 merge table.
func TestPolicyMerge(t *testing.T) {
	g := Rules{AllowedRegistries: []string{"ghcr.io/"}, AllowedK8sNamespaces: []string{"a*", "b"}, RequiredLabels: []string{"x"},
		AllowedSafetyProfiles: []string{"managed_rollout", "as_is"}, AllowedBindPrefixes: []string{"/data"}, AllowPrivileged: true}
	n := Rules{AllowedRegistries: []string{"ghcr.io/acme/"}, AllowedK8sNamespaces: []string{"ab", "zz"}, RequiredLabels: []string{"y", "x"},
		ProtectedDBApproval: true, AllowedSafetyProfiles: []string{"as_is", "parachute"}, AllowedBindPrefixes: []string{"/data/x"}}
	e := Merge(g, &n)
	if !slices.Equal(e.AllowedRegistries, n.AllowedRegistries) || !slices.Equal(e.AllowedK8sNamespaces, []string{"ab"}) ||
		!slices.Equal(e.RequiredLabels, []string{"x", "y"}) || !e.ProtectedDBApproval || !slices.Equal(e.AllowedSafetyProfiles, []string{"as_is"}) ||
		!slices.Equal(e.AllowedBindPrefixes, []string{"/data/x"}) || e.AllowPrivileged {
		t.Fatalf("%+v", e)
	}
	n.AllowedK8sNamespaces = []string{"zz"}
	if got := Merge(g, &n).AllowedK8sNamespaces; !slices.Equal(got, []string{"-"}) {
		t.Fatal(got)
	}
	if Validate(g, Rules{AllowedRegistries: []string{"evil/"}}) == "" || Validate(g, Rules{AllowedBindPrefixes: []string{"/etc"}}) == "" ||
		Validate(g, Rules{AllowedRegistries: []string{"ghcr.io/x/"}, AllowedBindPrefixes: []string{"/data/y"}}) != "" {
		t.Fatal("validate")
	}
}
