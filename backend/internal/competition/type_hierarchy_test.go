package competition

import "testing"

func TestHierarchyMoveRenameAndMerge(t *testing.T) {
	s, ctx := setup(t)
	for _, name := range []string{"注入", "SQL注入", "盲注", "XSS"} {
		if _, err := s.DB.Exec("INSERT INTO question_types(name) VALUES(?)", name); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"注入", "WEB"}, {"SQL注入", "注入"}, {"盲注", "SQL注入"}, {"XSS", "WEB"}} {
		if err := s.MoveQuestionType(ctx, "admin", pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"WEB", "盲注"}, {"WEB", "WEB"}, {"WEB", "不存在"}} {
		if err := s.MoveQuestionType(ctx, "admin", pair[0], pair[1]); err == nil {
			t.Fatal("invalid parent accepted", pair)
		}
	}
	if _, err := s.ChangeTaxonomy(ctx, "admin", "type", TaxonomyChange{Source: "注入", Target: "SQL注入", Mode: "merge"}); err == nil {
		t.Fatal("merged parent into descendant")
	}
	if _, err := s.ChangeTaxonomy(ctx, "admin", "type", TaxonomyChange{Source: "注入", Target: "注入漏洞", Mode: "rename"}); err != nil {
		t.Fatal(err)
	}
	parent := func(name string) string {
		var p string
		if err := s.DB.QueryRow("SELECT COALESCE(parent_name,'') FROM question_types WHERE name=?", name).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := s.ChangeTaxonomy(ctx, "admin", "type", TaxonomyChange{Source: "WEB", Target: "Web", Mode: "rename"}); err != nil {
		t.Fatal(err)
	}
	if got := parent("XSS"); got != "Web" {
		t.Fatal("case-only rename broke canonical parent", got)
	}
	if got := parent("SQL注入"); got != "注入漏洞" {
		t.Fatal("rename lost parent link", got)
	}
	if _, err := s.ChangeTaxonomy(ctx, "admin", "type", TaxonomyChange{Source: "注入漏洞", Target: "XSS", Mode: "merge"}); err != nil {
		t.Fatal(err)
	}
	if got := parent("SQL注入"); got != "XSS" {
		t.Fatal("merge lost children", got)
	}
	if got := parent("盲注"); got != "SQL注入" {
		t.Fatal("merge flattened subtree", got)
	}
	if err := s.MoveQuestionType(ctx, "admin", "SQL注入", ""); err != nil {
		t.Fatal(err)
	}
	if got := parent("SQL注入"); got != "" {
		t.Fatal("move to root failed", got)
	}
	if _, err := s.DB.Exec("DELETE FROM question_types WHERE name='SQL注入'"); err == nil {
		t.Fatal("deleted parent despite children")
	}
}
