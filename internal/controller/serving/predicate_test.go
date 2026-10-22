package serving

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	servingv1alpha1 "github.com/modelsphere/llm-operator/api/serving/v1alpha1"
	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// predicateCase is one llmServicePredicate input. The base object is generation
// 1 with annotation a=1; the case describes what changes on the new object.
type predicateCase struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Op          string            `json:"op"`
	Phase       string            `json:"phase,omitempty"`
	Generation  int64             `json:"generation,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Deleting    bool              `json:"deleting,omitempty"`
	Want        bool              `json:"want"`
}

func TestLLMServicePredicate(t *testing.T) {
	cases.Run(t, "testdata/predicate", func(t *testing.T, tc predicateCase) {
		p := llmServicePredicate{}
		base := &servingv1alpha1.LLMService{}
		base.Generation = 1
		base.Annotations = map[string]string{"a": "1"}
		var got bool
		switch tc.Op {
		case "update":
			next := base.DeepCopy()
			if tc.Phase != "" {
				next.Status.Phase = servingv1alpha1.Phase(tc.Phase)
			}
			if tc.Generation != 0 {
				next.Generation = tc.Generation
			}
			if tc.Annotations != nil {
				next.Annotations = tc.Annotations
			}
			if tc.Deleting {
				now := metav1.Now()
				next.DeletionTimestamp = &now
			}
			got = p.Update(event.UpdateEvent{ObjectOld: base, ObjectNew: next})
		case "create":
			got = p.Create(event.CreateEvent{Object: base})
		case "delete":
			got = p.Delete(event.DeleteEvent{Object: base})
		default:
			t.Fatalf("%s: unknown op %q", tc.Name, tc.Op)
		}
		if got != tc.Want {
			t.Fatalf("%s: reconcile = %v, want %v", tc.Description, got, tc.Want)
		}
	})
}
