package mail

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanEveMailBody(t *testing.T) {
	raw := `<font size="12" color="#bfffffff">Hello<br><br>This is a <b>test</b> message &amp; greetings.<br/></font>`
	expected := "Hello\n\nThis is a test message & greetings."
	cleaned := CleanEveMailBody(raw)
	if cleaned != expected {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, cleaned)
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
