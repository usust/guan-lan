package competition

import (
	"errors"
	"reflect"
	"testing"
)

func TestTypeRenameMergeAndVersionProtection(t *testing.T) {
	s, ctx := setup(t)
	library := draft()
	library.Library = true
	library.Questions[0].Category = "数据分析题"
	library = saveDraft(t, s, ctx, library)
	contest := draft()
	contest.Questions[0].Category = "数据分析题"
	contest = publish(t, s, ctx, saveDraft(t, s, ctx, contest))
	if _, err := s.DB.Exec("INSERT INTO question_types(name) VALUES('数据分析')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeTaxonomy(ctx, "admin", "type", TaxonomyChange{Source: "数据分析题", Target: "数据分析", Mode: "rename"}); err == nil {
		t.Fatal("rename silently merged types")
	}
	n, err := s.ChangeTaxonomy(ctx, "admin", "type", TaxonomyChange{Source: "数据分析题", Target: "数据分析", Mode: "merge"})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	for _, original := range []Set{library, contest} {
		got, err := s.Get(ctx, original.ID, "admin", true)
		if err != nil {
			t.Fatal(err)
		}
		if got.Questions[0].Category != "数据分析" || got.Version != original.Version+1 || got.Status != original.Status {
			t.Fatalf("bad taxonomy update: %#v", got)
		}
		if !reflect.DeepEqual(got.Questions[0].Subtasks, original.Questions[0].Subtasks) {
			t.Fatal("subtasks changed")
		}
	}
	if _, err = s.Save(ctx, "admin", library); !errors.Is(err, ErrConflict) {
		t.Fatal("stale editor accepted", err)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM question_types WHERE name='数据分析题'").Scan(&count)
	if count != 0 {
		t.Fatal("source type retained")
	}
	if n, err = s.ChangeTaxonomy(ctx, "admin", "type", TaxonomyChange{Source: "WEB", Target: "Web", Mode: "rename"}); err != nil || n != 0 {
		t.Fatal("case rename", n, err)
	}
}

func TestTagMaintenanceAndDeduplication(t *testing.T) {
	s, ctx := setup(t)
	x := draft()
	x.Library = true
	x.Questions[0].Tags = []string{"JWT", "令牌伪造", "SQL注入"}
	x = saveDraft(t, s, ctx, x)
	y := draft()
	y.Questions[0].Tags = []string{"jwt"}
	y = publish(t, s, ctx, saveDraft(t, s, ctx, y))
	if _, err := s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "JWT", Target: "不存在", Mode: "merge"}); err == nil {
		t.Fatal("merged into nonexistent tag")
	}
	if err := s.LinkKnowledgeTypes(ctx, "admin", "令牌伪造", []string{"WEB"}); err != nil {
		t.Fatal(err)
	}
	n, err := s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "jwt", Target: "令牌伪造", Mode: "merge"})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	x1, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(x1.Questions[0].Tags, []string{"令牌伪造", "SQL注入"}) {
		t.Fatal(x1.Questions[0].Tags)
	}
	if _, err = s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "SQL注入", Target: "SQL 注入", Mode: "rename"}); err != nil {
		t.Fatal(err)
	}
	if n, err = s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "令牌伪造", Mode: "delete"}); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	y1, err := s.Get(ctx, y.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(y1.Questions[0].Tags) != 0 || y1.Version != y.Version+2 {
		t.Fatal(y1)
	}
	x2, _ := s.Get(ctx, x.ID, "admin", true)
	if !reflect.DeepEqual(x2.Questions[0].Tags, []string{"SQL 注入"}) {
		t.Fatal(x2.Questions[0].Tags)
	}
}

func TestBatchClassificationAtomicAndLibraryOnly(t *testing.T) {
	s, ctx := setup(t)
	x := draft()
	x.Library = true
	x = saveDraft(t, s, ctx, x)
	y := draft()
	y.Library = true
	y = saveDraft(t, s, ctx, y)
	contest := saveDraft(t, s, ctx, draft())
	for _, selection := range [][]LibrarySelection{
		{{x.ID, x.Version}, {y.ID, y.Version + 1}},
		{{x.ID, x.Version}, {contest.ID, contest.Version}},
		{{x.ID, x.Version}, {x.ID, x.Version}},
	} {
		if _, err := s.ReclassifyLibrary(ctx, "admin", "PWN", selection); err == nil {
			t.Fatal("invalid batch accepted")
		}
		got, _ := s.Get(ctx, x.ID, "admin", true)
		if got.Questions[0].Category != "WEB" || got.Version != x.Version {
			t.Fatal("partial update committed")
		}
	}
	n, err := s.ReclassifyLibrary(ctx, "admin", "PWN", []LibrarySelection{{x.ID, x.Version}, {y.ID, y.Version}})
	if n != 2 || err != nil {
		t.Fatal(n, err)
	}
	got, _ := s.Get(ctx, x.ID, "admin", true)
	if got.Questions[0].Category != "PWN" || got.Version != x.Version+1 {
		t.Fatal(got)
	}
	if _, err = s.ReclassifyLibrary(ctx, "admin", "PWN", []LibrarySelection{{x.ID, x.Version}}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale batch accepted", err)
	}
	if n, err = s.ReclassifyLibrary(ctx, "admin", "PWN", []LibrarySelection{{got.ID, got.Version}}); err != nil || n != 0 {
		t.Fatal("no-op", n, err)
	}
}
