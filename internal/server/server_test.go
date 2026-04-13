package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/newsamples/imapsync/internal/storage"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServer(t *testing.T) (*Server, *storage.Storage) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := storage.New(dbPath, log)
	require.NoError(t, err)

	server := New(store, log)
	return server, store
}

func TestListMailboxes(t *testing.T) {
	t.Run("empty mailboxes", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response []map[string]interface{}
		err := json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Empty(t, response)
	})

	t.Run("with mailboxes", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		err := store.SaveMailboxState(&storage.MailboxState{
			Name:        "INBOX",
			UIDValidity: 123,
			LastUID:     10,
			LastSync:    time.Now(),
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response []map[string]interface{}
		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Len(t, response, 1)
		assert.Equal(t, "INBOX", response[0]["name"])
	})
}

func TestListEmails(t *testing.T) {
	t.Run("empty mailbox", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err := json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, float64(0), response["total"])
		assert.Empty(t, response["emails"])
	})

	t.Run("with emails", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		err := store.SaveMailboxState(&storage.MailboxState{
			Name:        "INBOX",
			UIDValidity: 123,
			LastUID:     1,
			LastSync:    time.Now(),
		})
		require.NoError(t, err)

		err = store.SaveEmail(&storage.Email{
			UID:     1,
			Mailbox: "INBOX",
			Subject: "Test Email",
			From:    "sender@example.com",
			To:      []string{"recipient@example.com"},
			Date:    time.Now(),
			Size:    1024,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, float64(1), response["total"])
		assert.Equal(t, float64(1), response["page"])
		assert.Equal(t, float64(1), response["total_pages"])
		emails := response["emails"].([]interface{})
		assert.Len(t, emails, 1)
		email := emails[0].(map[string]interface{})
		assert.Equal(t, "Test Email", email["subject"])
	})

	t.Run("mailbox with slash", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		err := store.SaveMailboxState(&storage.MailboxState{
			Name:        "Archive/2024",
			UIDValidity: 123,
			LastUID:     1,
			LastSync:    time.Now(),
		})
		require.NoError(t, err)

		err = store.SaveEmail(&storage.Email{
			UID:     1,
			Mailbox: "Archive/2024",
			Subject: "Archived Email",
			From:    "sender@example.com",
			To:      []string{"recipient@example.com"},
			Date:    time.Now(),
			Size:    512,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/Archive%2F2024/emails", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, float64(1), response["total"])
		emails := response["emails"].([]interface{})
		assert.Len(t, emails, 1)
		email := emails[0].(map[string]interface{})
		assert.Equal(t, "Archived Email", email["subject"])
	})
}

func TestGetEmail(t *testing.T) {
	t.Run("existing email", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		rawMsg := []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test Email\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nThis is the email body content.")

		err := store.SaveEmail(&storage.Email{
			UID:        1,
			Mailbox:    "INBOX",
			Subject:    "Test Email",
			From:       "sender@example.com",
			To:         []string{"recipient@example.com"},
			Date:       time.Now(),
			Size:       1024,
			Body:       []byte("Test body"),
			Headers:    []byte("Header: value\r\n"),
			RawMessage: rawMsg,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/1", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, "Test Email", response["subject"])
		assert.Equal(t, "sender@example.com", response["from"])
		assert.Contains(t, response["body"], "This is the email body content")
	})

	t.Run("non-existent email", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/999", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid uid", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/invalid", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("mailbox with slash", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		rawMsg := []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test Email\r\nContent-Type: text/plain\r\n\r\nTest body")

		err := store.SaveEmail(&storage.Email{
			UID:        1,
			Mailbox:    "Archive/2024",
			Subject:    "Test Email",
			From:       "sender@example.com",
			To:         []string{"recipient@example.com"},
			Date:       time.Now(),
			Size:       1024,
			Body:       []byte("Test body"),
			Headers:    []byte("Header: value\r\n"),
			RawMessage: rawMsg,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/Archive%2F2024/emails/1", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, "Test Email", response["subject"])
		assert.Equal(t, "sender@example.com", response["from"])
	})

	t.Run("html email", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		rawMsg := []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: HTML Email\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<html><body><h1>Hello World</h1></body></html>")

		err := store.SaveEmail(&storage.Email{
			UID:        2,
			Mailbox:    "INBOX",
			Subject:    "HTML Email",
			From:       "sender@example.com",
			To:         []string{"recipient@example.com"},
			Date:       time.Now(),
			Size:       512,
			RawMessage: rawMsg,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/2", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Contains(t, response["body"], "<h1>Hello World</h1>")
	})
}

func TestDownloadEmail(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		raw := []byte("From: sender@example.com\r\nSubject: DL\r\n\r\nBody")
		err := store.SaveEmail(&storage.Email{
			UID:        1,
			Mailbox:    "INBOX",
			Subject:    "DL",
			From:       "sender@example.com",
			Date:       time.Now(),
			Size:       100,
			RawMessage: raw,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/1/download", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "message/rfc822", w.Header().Get("Content-Type"))
		assert.Equal(t, raw, w.Body.Bytes())
	})

	t.Run("not found", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/999/download", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid uid", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/abc/download", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("no raw message", func(t *testing.T) {
		server, store := setupTestServer(t)
		defer store.Close()

		err := store.SaveEmail(&storage.Email{
			UID:     5,
			Mailbox: "INBOX",
			Subject: "NoRaw",
			From:    "sender@example.com",
			Date:    time.Now(),
			Size:    100,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/5/download", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestGetEmail_Multipart(t *testing.T) {
	server, store := setupTestServer(t)
	defer store.Close()

	boundary := "boundary123"
	rawMsg := []byte(fmt.Sprintf("From: sender@example.com\r\nSubject: Multipart\r\nContent-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nPlain text part\r\n--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>HTML part</p>\r\n--%s--\r\n", boundary, boundary, boundary, boundary))

	err := store.SaveEmail(&storage.Email{
		UID:        10,
		Mailbox:    "INBOX",
		Subject:    "Multipart",
		From:       "sender@example.com",
		Date:       time.Now(),
		Size:       500,
		RawMessage: rawMsg,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/10", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	// HTML part takes priority.
	assert.Contains(t, response["body"], "HTML part")
}

func TestDecodeBody(t *testing.T) {
	s := &Server{log: logrus.New()}

	t.Run("quoted-printable", func(t *testing.T) {
		result := s.decodeBody([]byte("Hello=20World"), "quoted-printable")
		assert.Equal(t, "Hello World", string(result))
	})

	t.Run("base64", func(t *testing.T) {
		original := []byte("Hello, World!")
		encoded := base64.StdEncoding.EncodeToString(original)
		result := s.decodeBody([]byte(encoded), "base64")
		assert.Equal(t, original, result)
	})

	t.Run("base64 invalid", func(t *testing.T) {
		input := []byte("not!!!valid!!!base64")
		result := s.decodeBody(input, "base64")
		assert.Equal(t, input, result)
	})

	t.Run("case insensitive", func(t *testing.T) {
		original := []byte("test data")
		encoded := base64.StdEncoding.EncodeToString(original)
		result := s.decodeBody([]byte(encoded), "BASE64")
		assert.Equal(t, original, result)
	})

	t.Run("no encoding", func(t *testing.T) {
		input := []byte("plain text")
		result := s.decodeBody(input, "")
		assert.Equal(t, input, result)
	})
}

func TestListEmails_Pagination(t *testing.T) {
	server, store := setupTestServer(t)
	defer store.Close()

	for i := 1; i <= 5; i++ {
		err := store.SaveEmail(&storage.Email{
			UID:     uint32(i),
			Mailbox: "INBOX",
			Subject: fmt.Sprintf("Email %d", i),
			From:    "sender@example.com",
			Date:    time.Now(),
			Size:    100,
		})
		require.NoError(t, err)
	}

	t.Run("valid page and limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails?page=2&limit=2", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		var response map[string]interface{}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
		assert.Equal(t, float64(2), response["page"])
		assert.Equal(t, float64(2), response["limit"])
		emails := response["emails"].([]interface{})
		assert.Len(t, emails, 2)
	})

	t.Run("invalid page defaults to 1", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails?page=invalid", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		var response map[string]interface{}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
		assert.Equal(t, float64(1), response["page"])
	})

	t.Run("limit too large defaults to 50", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails?limit=300", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		var response map[string]interface{}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
		assert.Equal(t, float64(50), response["limit"])
	})
}

func TestRun_InvalidAddr(t *testing.T) {
	server, store := setupTestServer(t)
	defer store.Close()
	err := server.Run("127.0.0.1:99999")
	assert.Error(t, err)
}

func TestGetEmail_EmptyBody(t *testing.T) {
	server, store := setupTestServer(t)
	defer store.Close()

	err := store.SaveEmail(&storage.Email{
		UID:     20,
		Mailbox: "INBOX",
		Subject: "Empty Body Email",
		From:    "sender@example.com",
		Date:    time.Now(),
		Size:    100,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/20", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	assert.Equal(t, "Empty Body Email", response["subject"])
}

func TestGetEmail_InvalidContentType(t *testing.T) {
	server, store := setupTestServer(t)
	defer store.Close()

	rawMsg := []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Invalid CT\r\nContent-Type: notavalidtype\r\n\r\nbody content here")

	err := store.SaveEmail(&storage.Email{
		UID:        30,
		Mailbox:    "INBOX",
		Subject:    "Invalid CT",
		From:       "sender@example.com",
		Date:       time.Now(),
		Size:       100,
		RawMessage: rawMsg,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/30", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	assert.Equal(t, "Invalid CT", response["subject"])
}

func TestGetEmail_NestedMultipart(t *testing.T) {
	server, store := setupTestServer(t)
	defer store.Close()

	outer := "outer123"
	inner := "inner456"
	rawMsg := []byte(fmt.Sprintf("From: sender@example.com\r\nSubject: Nested Multipart\r\nContent-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n--%s\r\nContent-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nPlain text from nested\r\n--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>HTML from nested</p>\r\n--%s--\r\n--%s--\r\n", outer, outer, inner, inner, inner, inner, outer))

	err := store.SaveEmail(&storage.Email{
		UID:        40,
		Mailbox:    "INBOX",
		Subject:    "Nested Multipart",
		From:       "sender@example.com",
		Date:       time.Now(),
		Size:       500,
		RawMessage: rawMsg,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes/INBOX/emails/40", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	assert.Contains(t, response["body"], "HTML from nested")
}

func TestServeUI(t *testing.T) {
	server, store := setupTestServer(t)
	defer store.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, w.Body.String(), "Email Browser")
}
