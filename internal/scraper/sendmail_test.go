package scraper

import (
	"strings"
	"testing"
)

func TestBuildBodies_PlainText(t_ *testing.T) {
	html, text := buildBodies("Hello Acme GmbH,\n\nWe make ironing systems.")
	if text != "Hello Acme GmbH,\n\nWe make ironing systems." {
		t_.Errorf("text part altered plaintext: %q", text)
	}
	if !strings.Contains(html, "<br>") {
		t_.Errorf("plaintext HTML alternative should convert newlines to <br>: %q", html)
	}
	if !strings.Contains(html, "Acme GmbH") {
		t_.Errorf("HTML alternative missing content: %q", html)
	}
}

func TestBuildBodies_HTMLInput(t_ *testing.T) {
	in := "<p>Hi <b>Acme</b></p><p>Call us &amp; save.</p>"
	html, text := buildBodies(in)
	if html != in {
		t_.Errorf("HTML input should be used verbatim for HTML part, got %q", html)
	}
	if strings.Contains(text, "<") {
		t_.Errorf("text part still contains tags: %q", text)
	}
	if !strings.Contains(text, "Call us & save.") {
		t_.Errorf("text part should unescape entities: %q", text)
	}
}

func TestBuildBodies_DetectsTags(t_ *testing.T) {
	if !htmlTagRe.MatchString("<p>x</p>") {
		t_.Error("should detect a <p> tag")
	}
	if htmlTagRe.MatchString("price < 5 and x > 3") {
		t_.Error("bare comparison operators should not be treated as HTML")
	}
}

func TestBodyPreview_Truncates(t_ *testing.T) {
	long := strings.Repeat("word ", 100)
	p := bodyPreview(long, 20)
	if len([]rune(p)) > 21 { // 20 chars + ellipsis
		t_.Errorf("preview not truncated: %q", p)
	}
	if !strings.HasSuffix(p, "…") {
		t_.Errorf("truncated preview should end with ellipsis: %q", p)
	}
}
