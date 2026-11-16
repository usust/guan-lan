package competition

import (
	"errors"
	"reflect"
	"testing"
)

func TestKnowledgeHierarchyLinksAndMerge(t *testing.T) {
	s, ctx := setup(t)
	for _, p := range [][3]string{{"身份认证", "", "WEB"}, {"JWT", "身份认证", "WEB"}, {"令牌伪造", "", "PWN"}, {"签名校验", "JWT", "WEB"}} {
		if err := s.SetKnowledge(ctx, "admin", p[0], p[1], true, p[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetKnowledge(ctx, "admin", "无归属", "", true); err == nil {
		t.Fatal("accepted unowned creation")
	}
	if err := s.SetKnowledge(ctx, "admin", "身份认证", "签名校验", false); err == nil {
		t.Fatal("cycle accepted")
	}
	for _, owners := range [][]string{{"WEB", "PWN"}, {}, {"不存在"}} {
		if err := s.LinkKnowledgeTypes(ctx, "admin", "JWT", owners); err == nil {
			t.Fatal("invalid ownership accepted", owners)
		}
	}
	points, err := s.KnowledgePoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	find := func(name string) KnowledgePoint {
		t.Helper()
		for _, p := range points {
			if p.Name == name {
				return p
			}
		}
		t.Fatal("missing point", name)
		return KnowledgePoint{}
	}
	if find("JWT").Owner != "WEB" || !reflect.DeepEqual(find("JWT").Types, []string{"WEB"}) {
		t.Fatal("ownership changed after invalid request", points)
	}
	x := draft()
	x.Library = true
	x.Questions[0].Tags = []string{"JWT", "令牌伪造"}
	x = saveDraft(t, s, ctx, x)
	if _, err = s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "JWT", Target: "jwt", Mode: "rename"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "身份认证", Target: "签名校验", Mode: "merge"}); err == nil {
		t.Fatal("merge into descendant accepted")
	}
	if _, err = s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "jwt", Mode: "delete"}); err == nil {
		t.Fatal("deleted non-leaf")
	}
	if n, err := s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "jwt", Target: "令牌伪造", Mode: "merge"}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	points, err = s.KnowledgePoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if find("签名校验").Parent != "令牌伪造" || find("签名校验").Owner != "PWN" {
		t.Fatal("merge lost subtree ownership", points)
	}
	got, _ := s.Get(ctx, x.ID, "admin", true)
	if !reflect.DeepEqual(got.Questions[0].Tags, []string{"令牌伪造"}) || got.Version != x.Version+2 {
		t.Fatal("question references/versions", got)
	}
	if _, err = s.ChangeTaxonomy(ctx, "admin", "type", TaxonomyChange{Source: "PWN", Target: "逆向", Mode: "merge"}); err != nil {
		t.Fatal(err)
	}
	points, err = s.KnowledgePoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if find("签名校验").Owner != "逆向" || find("令牌伪造").Owner != "逆向" {
		t.Fatal("type merge lost ownership", points)
	}
	if err = s.LinkKnowledgeTypes(ctx, "admin", "签名校验", []string{"WEB"}); err != nil {
		t.Fatal(err)
	}
	points, _ = s.KnowledgePoints(ctx)
	if find("签名校验").Parent != "" || find("签名校验").Owner != "WEB" {
		t.Fatal("cross-type move did not detach", points)
	}
	if _, err = s.ChangeTaxonomy(ctx, "admin", "tag", TaxonomyChange{Source: "令牌伪造", Mode: "delete"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, x.ID, "admin", true)
	if len(got.Questions[0].Tags) != 0 {
		t.Fatal("delete left references")
	}
}

func TestLibraryKnowledgeOnlyUpdatePreservesContentAndRejectsStale(t *testing.T) {
	s, ctx := setup(t)
	if err := s.SetKnowledge(ctx, "admin", "SQL 注入", "", true, "WEB"); err != nil {
		t.Fatal(err)
	}
	x := draft()
	x.Library = true
	x = saveDraft(t, s, ctx, x)
	before := x.Questions[0]
	version, err := s.UpdateLibraryKnowledge(ctx, "admin", x.ID, x.Version, []string{" SQL 注入 ", "SQL 注入"})
	if err != nil || version != x.Version+1 {
		t.Fatal(version, err)
	}
	got, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	expected := before
	expected.Tags = []string{"SQL 注入"}
	if !reflect.DeepEqual(got.Questions[0], expected) {
		t.Fatal("non-knowledge content changed", got.Questions[0])
	}
	if _, err = s.UpdateLibraryKnowledge(ctx, "admin", x.ID, x.Version, []string{}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale write accepted", err)
	}
	if _, err = s.UpdateLibraryKnowledge(ctx, "admin", x.ID, version, []string{"不存在"}); err == nil {
		t.Fatal("unknown knowledge accepted")
	}
	unchanged, _ := s.Get(ctx, x.ID, "admin", true)
	if unchanged.Version != version {
		t.Fatal("failed update advanced version")
	}
	contest := saveDraft(t, s, ctx, draft())
	if _, err = s.UpdateLibraryKnowledge(ctx, "admin", contest.ID, contest.Version, []string{}); err == nil {
		t.Fatal("competition updated through library API")
	}
	if _, err = s.UpdateLibraryKnowledge(ctx, "admin", x.ID, version, []string{}); err != nil {
		t.Fatal("cannot clear knowledge", err)
	}
}
