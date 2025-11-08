package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	dbPath := tmpDir + "/test.db"
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

		rawMsg := []byte("From: sender@example.com\r\n" +
			"To: recipient@example.com\r\n" +
			"Subject: Test Email\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"\r\n" +
			"This is the email body content.")

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

		rawMsg := []byte("From: sender@example.com\r\n" +
			"To: recipient@example.com\r\n" +
			"Subject: Test Email\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" +
			"Test body")

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

		rawMsg := []byte("From: sender@example.com\r\n" +
			"To: recipient@example.com\r\n" +
			"Subject: HTML Email\r\n" +
			"Content-Type: text/html; charset=utf-8\r\n" +
			"\r\n" +
			"<html><body><h1>Hello World</h1></body></html>")

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
