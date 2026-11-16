package competition

import "testing"

func TestCustomDifficultyValidation(t *testing.T) {
	s, ctx := setup(t)
	if _, err := s.DB.Exec("INSERT INTO difficulties(code,name,position) VALUES('CUSTOM_TEST','专家',3)"); err != nil {
		t.Fatal(err)
	}
	x := draft()
	x.Questions[0].Difficulty = "CUSTOM_TEST"
	saved := saveDraft(t, s, ctx, x)
	if saved.Questions[0].Difficulty != "CUSTOM_TEST" {
		t.Fatal(saved.Questions[0].Difficulty)
	}
	x = draft()
	x.Questions[0].Difficulty = "UNKNOWN"
	if _, err := s.Save(ctx, "admin", x); err == nil {
		t.Fatal("unknown difficulty accepted")
	}
}
