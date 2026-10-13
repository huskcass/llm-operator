package helm

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// namespaceGuard is a post-renderer that refuses a chart writing outside its
// release's namespace: an object naming another namespace, or a cluster-scoped
// one. The operator applies charts with cluster-wide permissions, and nothing
// else keeps a chart from using them elsewhere. Helm 4 post-renders hooks with
// the templates, so it sees those too. It passes the manifests on unchanged.
type namespaceGuard struct {
	namespace string
	mapper    meta.RESTMapper
}

func (g namespaceGuard) Run(in *bytes.Buffer) (*bytes.Buffer, error) {
	r := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(in.Bytes())))
	for {
		doc, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("rendered manifests: %w", err)
		}
		var obj map[string]any
		if err := yaml.Unmarshal(doc, &obj); err != nil {
			return nil, fmt.Errorf("rendered manifests: %w", err)
		}
		if len(obj) == 0 {
			continue
		}
		if err := g.check(&unstructured.Unstructured{Object: obj}); err != nil {
			return nil, err
		}
	}
	return bytes.NewBuffer(append([]byte(nil), in.Bytes()...)), nil
}

func (g namespaceGuard) check(u *unstructured.Unstructured) error {
	if u.IsList() {
		return u.EachListItem(func(item runtime.Object) error {
			return g.check(item.(*unstructured.Unstructured))
		})
	}
	gvk := u.GroupVersionKind()
	if gvk.Kind == "" {
		return fmt.Errorf("chart renders an object with no kind")
	}
	what := fmt.Sprintf("%s %q", gvk.Kind, u.GetName())
	m, err := g.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("chart renders %s, and whether it is namespaced cannot be told: %w", what, err)
	}
	if m.Scope.Name() != meta.RESTScopeNameNamespace {
		return fmt.Errorf("chart renders %s, which is cluster-scoped -- a chart may only write into its release's namespace %s", what, g.namespace)
	}
	if ns := u.GetNamespace(); ns != "" && ns != g.namespace {
		return fmt.Errorf("chart renders %s into namespace %s -- a chart may only write into its release's namespace %s", what, ns, g.namespace)
	}
	return nil
}
