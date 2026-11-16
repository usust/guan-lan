package competition

import (
	"reflect"
	"strings"
	"testing"
)

func TestLibraryTaxonomyRoundTrip(t *testing.T) {
	s, ctx := setup(t)
	x := draft()
	x.Library = true
	x.Questions[0].Category = " web "
	x.Questions[0].Tags = []string{" SQL 注入 ", "JWT", "jwt"}
	x = saveDraft(t, s, ctx, x)
	q := x.Questions[0]
	if q.Category != "WEB" || !reflect.DeepEqual(q.Tags, []string{"SQL 注入", "JWT"}) {
		t.Fatalf("normalization: %#v", q)
	}
	items, err := s.ListLibrary(ctx)
	if err != nil || len(items) != 1 || len(items[0].Questions) != 1 {
		t.Fatalf("library metadata: %#v %v", items, err)
	}
	metadata := items[0].Questions[0]
	if !reflect.DeepEqual(metadata.Tags, q.Tags) || metadata.Category != q.Category || len(metadata.Subtasks) != 0 || metadata.Environment != nil {
		t.Fatalf("unsafe/incomplete metadata: %#v", metadata)
	}
	x.Questions[0].Tags = []string{}
	x = saveDraft(t, s, ctx, x)
	if len(x.Questions[0].Tags) != 0 {
		t.Fatal("clearing tags failed")
	}
}

func TestInvalidTaxonomyTags(t *testing.T) {
	for _, tags := range [][]string{{" "}, {strings.Repeat("字", 33)}, make([]string, 21)} {
		s, ctx := setup(t)
		x := draft()
		x.Questions[0].Tags = tags
		if _, err := s.Save(ctx, "admin", x); err == nil {
			t.Fatalf("accepted invalid tags: %#v", tags)
		}
	}
}
