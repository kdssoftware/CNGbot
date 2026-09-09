package mail

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanEveMailBody(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{
			name:     "basic formatting tags stripped and br to newline",
			raw:      `<font size="12" color="#bfffffff">Hello<br><br>This is a <b>test</b> message &amp; greetings.<br/></font>`,
			expected: "Hello\n\nThis is a test message & greetings.",
		},
		{
			name:     "preserve https link",
			raw:      `<font size="12">Join our Discord: <a href="https://discord.gg/abc">https://discord.gg/abc</a></font>`,
			expected: `Join our Discord: <a href="https://discord.gg/abc">https://discord.gg/abc</a>`,
		},
		{
			name:     "preserve http link",
			raw:      `Visit <a href="http://eve-gate.net">our site</a> for details.`,
			expected: `Visit <a href="http://eve-gate.net">our site</a> for details.`,
		},
		{
			name:     "strip non-http/https eve links like showinfo and fitting",
			raw:      `Meet at <a href="showinfo:1373//10000002">Jita IV - 4</a> in your <a href="fitting:1234:...">Rifter</a>.`,
			expected: `Meet at Jita IV - 4 in your Rifter.`,
		},
		{
			name:     "mixed http and non-http links",
			raw:      `Check <a href="showinfo:1373//10000002">Jita</a> and <a href="https://zkillboard.com">Zkill</a> and <a href="http://example.com">Example</a>.`,
			expected: `Check Jita and <a href="https://zkillboard.com">Zkill</a> and <a href="http://example.com">Example</a>.`,
		},
		{
			name:     "nested formatting inside allowed link stripped",
			raw:      `<a href="https://example.com"><b>Bold Link</b></a>`,
			expected: `<a href="https://example.com">Bold Link</a>`,
		},
		{
			name:     "case insensitivity of scheme and tags",
			raw:      `<a href="HTTPS://EXAMPLE.COM">Capital Scheme</a> and <A HREF="HTTP://EXAMPLE.COM">Capital Tag</A>`,
			expected: `<a href="HTTPS://EXAMPLE.COM">Capital Scheme</a> and <A HREF="HTTP://EXAMPLE.COM">Capital Tag</A>`,
		},
		{
			name:     "extra attributes in a tag preserved",
			raw:      `<a target="_blank" href="https://example.com" class="external">Link</a>`,
			expected: `<a target="_blank" href="https://example.com" class="external">Link</a>`,
		},
		{
			name:     "other schemes stripped",
			raw:      `Contact <a href="mailto:pilot@eve.com">pilot</a> or <a href="javascript:void(0)">click</a>.`,
			expected: `Contact pilot or click.`,
		},
		{
			name:     "anchor without href or empty href stripped",
			raw:      `Anchor <a name="top">Top</a> and <a href="">Empty</a>.`,
			expected: `Anchor Top and Empty.`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned := CleanEveMailBody(tt.raw)
			if cleaned != tt.expected {
				t.Fatalf("expected:\n%q\ngot:\n%q", tt.expected, cleaned)
			}
		})
	}
}

func TestSendEveMail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/characters/12345/mail") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req SendMailRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if req.Subject != "Test Subject" || req.Recipients[0].RecipientID != 999 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := server.Client()
	// Test sending with character ID 12345
	// In the real code, it points to https://esi.evetech.net, but we can verify param validation
	err := SendEveMail(nil, 12345, 999, "Test Subject", "Test Body")
	if err == nil {
		t.Fatal("expected error with nil client")
	}

	err = SendEveMail(client, 0, 999, "Test Subject", "Test Body")
	if err == nil {
		t.Fatal("expected error with 0 sender character ID and empty config")
	}
}
