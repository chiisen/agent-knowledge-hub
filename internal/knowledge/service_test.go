package knowledge

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateKnowledge(t *testing.T) {
	valid := Knowledge{Title: "標題", Content: "內容", Type: "decision", Scope: "custom", Status: "draft", Source: "來源"}
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Knowledge)
	}{
		{"title", func(k *Knowledge) { k.Title = " " }},
		{"content", func(k *Knowledge) { k.Content = "" }},
		{"type", func(k *Knowledge) { k.Type = "" }},
		{"scope", func(k *Knowledge) { k.Scope = "" }},
		{"source", func(k *Knowledge) { k.Source = "" }},
		{"status", func(k *Knowledge) { k.Status = "unknown" }},
		{"large", func(k *Knowledge) { k.Content = strings.Repeat("x", 65537) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := valid
			tc.change(&k)
			if err := validate(k); !errors.Is(err, ErrInvalid) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestPatchConfirmedKnowledgeRequiresSeparateConfirmation(t *testing.T) {
	k := Knowledge{Title: "title", Content: "old", Type: "type", Scope: "scope", Source: "source", Status: "confirmed"}
	changed, confirmed := "new", "confirmed"
	if _, err := applyPatch(k, Patch{Content: &changed, Status: &confirmed}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("combined confirmation error = %v", err)
	}
	deprecated := "deprecated"
	if _, err := applyPatch(k, Patch{Content: &changed, Status: &deprecated}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("changed confirmed content must first become draft: %v", err)
	}
	updated, err := applyPatch(k, Patch{Content: &changed})
	if err != nil || updated.Status != "draft" || updated.Content != "new" {
		t.Fatalf("update = %+v, err = %v", updated, err)
	}
	updated, err = applyPatch(updated, Patch{Status: &confirmed})
	if err != nil || updated.Status != "confirmed" {
		t.Fatalf("confirmation = %+v, err = %v", updated, err)
	}
	if _, err := applyPatch(k, Patch{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty patch = %v", err)
	}
}
