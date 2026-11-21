// Package policy evaluates revisions against merged rules (05 Policy). Evaluate is pure.
package policy

import (
	"fmt"
	"path"
	"slices"
	"strings"

	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

type Rules struct {
	AllowedRegistries     []string `json:"allowed_registries"`
	AllowedK8sNamespaces  []string `json:"allowed_k8s_namespaces"`
	RequiredLabels        []string `json:"required_labels"`
	ProtectedDBApproval   bool     `json:"protected_db_approval"`
	AllowedSafetyProfiles []string `json:"allowed_safety_profiles"`
	AllowedBindPrefixes   []string `json:"allowed_bind_prefixes"`
	AllowPrivileged       bool     `json:"allow_privileged"`
	AllowedProducts       []string `json:"allowed_products"` // POL-R15: products that may place apps here; [] = any
}

type Violation struct {
	Rule    string `json:"rule"`
	Service string `json:"service,omitempty"`
	Message string `json:"message"`
}

type Result struct {
	Allowed          bool        `json:"allowed"`
	RequiresApproval bool        `json:"requires_approval"`
	Violations       []Violation `json:"violations"`
	ApprovalReasons  []string    `json:"approval_reasons"`
}

// Input is the parsed (pre-render) revision. DBChanged: first apply, a database workload's spec differs
// from the deployed revision, or the profile changes (POL-R9); the caller computes it.
type Input struct {
	K8sNamespace     string
	Profile          string
	Objects          []map[string]any
	DatabaseServices []string
	DBChanged        bool
}

func nn(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Norm makes nil lists serialise as [].
func (r Rules) Norm() Rules {
	r.AllowedRegistries, r.AllowedK8sNamespaces, r.RequiredLabels = nn(r.AllowedRegistries), nn(r.AllowedK8sNamespaces), nn(r.RequiredLabels)
	r.AllowedSafetyProfiles, r.AllowedBindPrefixes, r.AllowedProducts = nn(r.AllowedSafetyProfiles), nn(r.AllowedBindPrefixes), nn(r.AllowedProducts)
	return r
}

func glob(pat, s string) bool {
	if p, ok := strings.CutSuffix(pat, "*"); ok {
		return strings.HasPrefix(s, p)
	}
	return pat == s
}

func under(p, prefix string) bool {
	p, prefix = path.Clean(p), path.Clean(prefix)
	return p == prefix || prefix == "/" || strings.HasPrefix(p, prefix+"/")
}

func canon(img string) string {
	first, _, ok := strings.Cut(img, "/")
	switch {
	case !ok:
		return "docker.io/library/" + img
	case strings.ContainsAny(first, ".:") || first == "localhost":
		return img
	}
	return "docker.io/" + img
}

// Merge combines global and namespace rules (05 table). A namespace list that is empty/omitted inherits
// the global one; ns may be nil (no namespace policy).
func Merge(g Rules, ns *Rules) Rules {
	if ns == nil {
		return g.Norm()
	}
	e := g
	if len(ns.AllowedRegistries) > 0 {
		e.AllowedRegistries = ns.AllowedRegistries
	}
	if len(ns.AllowedK8sNamespaces) > 0 {
		if len(g.AllowedK8sNamespaces) == 0 {
			e.AllowedK8sNamespaces = ns.AllowedK8sNamespaces
		} else {
			e.AllowedK8sNamespaces = nil
			for _, n := range ns.AllowedK8sNamespaces {
				if slices.ContainsFunc(g.AllowedK8sNamespaces, func(p string) bool { return glob(p, n) }) {
					e.AllowedK8sNamespaces = append(e.AllowedK8sNamespaces, n)
				}
			}
			if len(e.AllowedK8sNamespaces) == 0 {
				e.AllowedK8sNamespaces = []string{"-"} // empty would mean "any"; "-" is not a valid namespace name
			}
		}
	}
	e.RequiredLabels = append(slices.Clone(g.RequiredLabels), ns.RequiredLabels...)
	slices.Sort(e.RequiredLabels)
	e.RequiredLabels = slices.Compact(e.RequiredLabels)
	e.ProtectedDBApproval = g.ProtectedDBApproval || ns.ProtectedDBApproval
	if len(ns.AllowedSafetyProfiles) > 0 {
		e.AllowedSafetyProfiles = slices.DeleteFunc(slices.Clone(g.AllowedSafetyProfiles), func(p string) bool { return !slices.Contains(ns.AllowedSafetyProfiles, p) })
	}
	if len(ns.AllowedBindPrefixes) > 0 {
		e.AllowedBindPrefixes = ns.AllowedBindPrefixes // Validate guarantees they are within global
	}
	e.AllowPrivileged = g.AllowPrivileged && ns.AllowPrivileged
	if len(ns.AllowedProducts) > 0 { // intersection; "-" is not a valid slug, so an empty intersection allows nothing
		e.AllowedProducts = nil
		for _, p := range ns.AllowedProducts {
			if len(g.AllowedProducts) == 0 || slices.Contains(g.AllowedProducts, p) {
				e.AllowedProducts = append(e.AllowedProducts, p)
			}
		}
		if len(e.AllowedProducts) == 0 {
			e.AllowedProducts = []string{"-"}
		}
	}
	return e.Norm()
}

// ProductAllowed is POL-R15: whether an application of product may be placed in a namespace with these effective rules.
func (r Rules) ProductAllowed(product string) bool {
	return len(r.AllowedProducts) == 0 || slices.Contains(r.AllowedProducts, product)
}

// Validate rejects a namespace rules document that widens the global one (registries, bind prefixes).
func Validate(g, ns Rules) string {
	for _, r := range ns.AllowedRegistries {
		if len(g.AllowedRegistries) > 0 && !slices.ContainsFunc(g.AllowedRegistries, func(p string) bool { return strings.HasPrefix(r, p) }) {
			return "allowed_registries entry " + r + " is outside the global list"
		}
	}
	for _, b := range ns.AllowedBindPrefixes {
		if !slices.ContainsFunc(g.AllowedBindPrefixes, func(p string) bool { return under(b, p) }) {
			return "allowed_bind_prefixes entry " + b + " is outside the global list"
		}
	}
	return ""
}

var forbiddenPrefixes = []string{"control-8/", "app.kubernetes.io/managed-by", "argocd.argoproj.io/"} // K8S-025

// Evaluate checks the revision input against the effective rules (POL-R1..R4, R6, R7, R9, R12..R14).
func Evaluate(r Rules, in Input) Result {
	res := Result{Violations: []Violation{}, ApprovalReasons: []string{}}
	add := func(rule, svc, msg string) { res.Violations = append(res.Violations, Violation{rule, svc, msg}) }

	if !slices.Contains(r.AllowedSafetyProfiles, in.Profile) { // R3
		add("safety_profile_not_allowed", "", "profile "+in.Profile+" is not allowed")
	}
	if n := in.K8sNamespace; strings.HasPrefix(n, "kube-") || n == "default" ||
		len(r.AllowedK8sNamespaces) > 0 && !slices.ContainsFunc(r.AllowedK8sNamespaces, func(p string) bool { return glob(p, n) }) { // R13
		add("namespace_not_allowed", "", "kubernetes namespace "+n+" is not allowed")
	}
	for _, o := range in.Objects {
		svc, kind := k8s.Name(o), k8s.Kind(o)
		if !k8s.Kinds[kind] { // R12, R14
			add("kind_not_allowed", svc, "kind "+kind+" is not allowed")
			continue
		}
		if n, _ := k8s.Dig(o, "metadata", "namespace").(string); n != "" && n != in.K8sNamespace { // K8S-022
			add("namespace_not_allowed", svc, "metadata.namespace must be empty or "+in.K8sNamespace)
		}
		for _, md := range []any{k8s.Dig(o, "metadata"), k8s.Dig(k8s.PodTemplate(o), "metadata")} { // R6
			for _, f := range []string{"labels", "annotations"} {
				m, _ := k8s.Dig(md, f).(map[string]any)
				for k := range m {
					if slices.ContainsFunc(forbiddenPrefixes, func(p string) bool { return strings.HasPrefix(k, p) }) {
						add("label_forbidden", svc, f+" key "+k+" is reserved")
					}
				}
			}
		}
		if !k8s.IsWorkload(kind) {
			continue
		}
		labels, _ := k8s.Dig(o, "metadata", "labels").(map[string]any)
		for _, l := range r.RequiredLabels { // R2
			if _, ok := labels[l]; !ok {
				add("label_missing", svc, "label "+l+" is required")
			}
		}
		for _, c := range k8s.Containers(o) { // R1
			img, _ := c["image"].(string)
			if len(r.AllowedRegistries) > 0 && !slices.ContainsFunc(r.AllowedRegistries, func(p string) bool { return strings.HasPrefix(canon(img), p) }) {
				add("registry_not_allowed", svc, "image "+img+" is not in an allowed registry")
			}
		}
		podChecks(r, svc, o, add)
	}
	if r.ProtectedDBApproval && len(in.DatabaseServices) > 0 && in.DBChanged { // R9
		res.RequiresApproval, res.ApprovalReasons = true, []string{"protected_database"}
	}
	res.Allowed = len(res.Violations) == 0
	return res
}

func podChecks(r Rules, svc string, o map[string]any, add func(rule, svc, msg string)) {
	ps := k8s.PodSpec(o)
	vols, _ := ps["volumes"].([]any)
	for _, v := range vols { // R4
		p, ok := k8s.Dig(v, "hostPath", "path").(string)
		if ok && (strings.Contains(p, "..") || !path.IsAbs(p) || !slices.ContainsFunc(r.AllowedBindPrefixes, func(b string) bool { return under(p, b) })) {
			add("host_path_not_allowed", svc, "hostPath "+p+" is not under an allowed prefix")
		}
	}
	if r.AllowPrivileged { // R7
		return
	}
	priv := func(what string) { add("privileged_not_allowed", svc, what+" requires allow_privileged") }
	for _, k := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if ps[k] == true {
			priv(k)
		}
	}
	for _, c := range k8s.Containers(o) {
		if k8s.Dig(c, "securityContext", "privileged") == true {
			priv("privileged: true")
		}
		if k8s.Dig(c, "securityContext", "allowPrivilegeEscalation") == true {
			priv("allowPrivilegeEscalation: true")
		}
		caps, _ := k8s.Dig(c, "securityContext", "capabilities", "add").([]any)
		for _, a := range caps {
			if slices.Contains([]any{"SYS_ADMIN", "NET_ADMIN", "ALL"}, a) {
				priv(fmt.Sprint("capability ", a))
			}
		}
		ports, _ := c["ports"].([]any)
		for _, p := range ports {
			if hp, _ := k8s.Dig(p, "hostPort").(float64); hp != 0 {
				priv("hostPort")
			}
		}
	}
}
