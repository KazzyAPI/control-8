// Package kubernetes holds the manifest side of the Kubernetes runtime: parse, render (K8S-020..026).
// Cluster writes arrive in M7; nothing here talks to a cluster.
package kubernetes

import (
	"fmt"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// Kinds is the K8S-021 allow-list (fixed in code); workloads carry a pod template.
var Kinds = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Job": true, "CronJob": true,
	"Service": true, "ConfigMap": true, "PersistentVolumeClaim": true, "ServiceAccount": true, "Ingress": true,
	"NetworkPolicy": true, "HorizontalPodAutoscaler": true, "PodDisruptionBudget": true}

var workloads = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Job": true, "CronJob": true}

func IsWorkload(kind string) bool { return workloads[kind] }

// Ref is one inventory entry (K8S-026).
type Ref struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

// Dig walks nested maps; nil if any step is missing.
func Dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func str(v any, keys ...string) string { s, _ := Dig(v, keys...).(string); return s }

// Kind returns the object's kind ("" if absent).
func Kind(o map[string]any) string { return str(o, "kind") }

// Name is "Kind/name" (the service name used in violations and database_services).
func Name(o map[string]any) string { return str(o, "kind") + "/" + str(o, "metadata", "name") }

func refOf(o map[string]any) Ref {
	g, _, ok := strings.Cut(str(o, "apiVersion"), "/")
	if !ok {
		g = ""
	}
	return Ref{g, str(o, "kind"), str(o, "metadata", "name")}
}

var docSep = regexp.MustCompile(`(?m)^---[ \t]*\r?$`)

// Parse splits multi-document YAML into objects (K8S-020). Any problem is an error naming the document.
func Parse(manifests string) ([]map[string]any, error) {
	var out []map[string]any
	seen := map[Ref]bool{}
	for i, doc := range docSep.Split(manifests, -1) {
		var o map[string]any
		if err := yaml.Unmarshal([]byte(doc), &o); err != nil {
			return nil, fmt.Errorf("document %d: %v", i+1, err)
		}
		if len(o) == 0 {
			return nil, fmt.Errorf("document %d: empty", i+1)
		}
		r := refOf(o)
		switch {
		case str(o, "apiVersion") == "" || r.Kind == "" || r.Name == "":
			return nil, fmt.Errorf("document %d: apiVersion, kind and metadata.name are required", i+1)
		case r.Kind == "List":
			return nil, fmt.Errorf("document %d: kind List is not supported", i+1)
		case seen[r]:
			return nil, fmt.Errorf("document %d: duplicate %s/%s", i+1, r.Kind, r.Name)
		}
		seen[r] = true
		out = append(out, o)
	}
	return out, nil
}

// PodTemplate returns the pod template of a workload (CronJob: spec.jobTemplate.spec.template), or nil.
func PodTemplate(o map[string]any) map[string]any {
	var t any
	if Kind(o) == "CronJob" {
		t = Dig(o, "spec", "jobTemplate", "spec", "template")
	} else if IsWorkload(Kind(o)) {
		t = Dig(o, "spec", "template")
	}
	m, _ := t.(map[string]any)
	return m
}

// PodSpec is the pod spec of a workload, or nil.
func PodSpec(o map[string]any) map[string]any {
	m, _ := Dig(PodTemplate(o), "spec").(map[string]any)
	return m
}

// Containers lists containers, initContainers and ephemeralContainers of a workload.
func Containers(o map[string]any) (out []map[string]any) {
	for _, k := range []string{"containers", "initContainers", "ephemeralContainers"} {
		l, _ := Dig(PodSpec(o), k).([]any)
		for _, c := range l {
			if m, ok := c.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return
}

// sub returns o[k] as a map, creating it when absent.
func sub(o map[string]any, k string) map[string]any {
	m, ok := o[k].(map[string]any)
	if !ok {
		m = map[string]any{}
		o[k] = m
	}
	return m
}

// Render strips server fields, sets the namespace and injects the K8S-024 labels on objects and pod
// templates (the revision label only on the object, so a structural no-op restarts nothing).
// Selectors are never touched. Returns the final objects and the inventory.
func Render(objs []map[string]any, stackID, ns string, rev int, profile string) ([]map[string]any, []Ref) {
	base := map[string]any{"app.kubernetes.io/managed-by": "control-8", "control-8/managed": "true",
		"control-8/stack": stackID, "control-8/namespace": ns, "control-8/safety-profile": profile}
	inv := []Ref{}
	for _, o := range objs {
		delete(o, "status")
		md := sub(o, "metadata")
		for _, k := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp"} {
			delete(md, k)
		}
		md["namespace"] = ns
		l := sub(md, "labels")
		for k, v := range base {
			l[k] = v
		}
		l["control-8/revision"] = fmt.Sprint(rev)
		if t := PodTemplate(o); t != nil {
			tl := sub(sub(t, "metadata"), "labels")
			for k, v := range base {
				tl[k] = v
			}
		}
		inv = append(inv, refOf(o))
	}
	return objs, inv
}

// databaseImages is Parachute's POL-003 list (04 "Database auto-detect"): last path component of the image repo.
var databaseImages = map[string]bool{"postgres": true, "postgresql": true, "postgis": true, "timescaledb": true, "mysql": true,
	"mariadb": true, "percona-server": true, "mongo": true, "mongodb": true, "redis": true, "valkey": true, "influxdb": true,
	"elasticsearch": true, "opensearch": true, "cassandra": true, "couchdb": true, "clickhouse-server": true, "neo4j": true}

// DetectDatabases returns workloads (Kind/name) with a container whose image repo is a known database.
func DetectDatabases(objs []map[string]any) (out []string) {
	for _, o := range objs {
		for _, c := range Containers(o) {
			img, _ := c["image"].(string)
			img, _, _ = strings.Cut(img, "@")
			repo := img
			if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
				repo = img[:i]
			}
			if databaseImages[repo[strings.LastIndex(repo, "/")+1:]] {
				out = append(out, Name(o))
				break
			}
		}
	}
	return
}
