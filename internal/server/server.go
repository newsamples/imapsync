package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/newsamples/imapsync/internal/storage"
	"github.com/sirupsen/logrus"
)

type Server struct {
	storage *storage.Storage
	log     *logrus.Logger
	router  *mux.Router
}

func New(store *storage.Storage, log *logrus.Logger) *Server {
	s := &Server{
		storage: store,
		log:     log,
		router:  mux.NewRouter(),
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/mailboxes", s.listMailboxes).Methods(http.MethodGet)
	api.HandleFunc("/mailboxes/{name:.*}/emails/{uid}/download", s.downloadEmail).Methods(http.MethodGet)
	api.HandleFunc("/mailboxes/{name:.*}/emails/{uid}", s.getEmail).Methods(http.MethodGet)
	api.HandleFunc("/mailboxes/{name:.*}/emails", s.listEmails).Methods(http.MethodGet)

	s.router.HandleFunc("/", s.serveUI).Methods(http.MethodGet)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) listMailboxes(w http.ResponseWriter, _ *http.Request) {
	mailboxes, err := s.storage.ListMailboxes()
	if err != nil {
		s.log.WithError(err).Error("Failed to list mailboxes")
		http.Error(w, "Failed to list mailboxes", http.StatusInternalServerError)
		return
	}

	response := make([]map[string]interface{}, 0, len(mailboxes))
	for _, name := range mailboxes {
		state, err := s.storage.GetMailboxState(name)
		if err != nil {
			s.log.WithError(err).Warnf("Failed to get state for mailbox %s", name)
			continue
		}

		count, err := s.storage.CountMessages(name)
		if err != nil {
			s.log.WithError(err).Warnf("Failed to count messages for mailbox %s", name)
			count = 0
		}

		item := map[string]interface{}{
			"name":  name,
			"count": count,
		}
		if state != nil {
			item["last_uid"] = state.LastUID
			item["last_sync"] = state.LastSync
		}

		response = append(response, item)
	}

	s.writeJSON(w, response)
}

func (s *Server) listEmails(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mailbox := vars["name"]

	// Parse pagination parameters
	page := 1
	limit := 50

	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 200 {
			limit = l
		}
	}

	offset := (page - 1) * limit

	// Get total count
	totalCount, err := s.storage.CountMessages(mailbox)
	if err != nil {
		s.log.WithError(err).Error("Failed to count messages")
		http.Error(w, "Failed to count messages", http.StatusInternalServerError)
		return
	}

	// Get paginated emails
	emails, err := s.storage.ListEmails(mailbox, limit, offset)
	if err != nil {
		s.log.WithError(err).Error("Failed to list emails")
		http.Error(w, "Failed to list emails", http.StatusInternalServerError)
		return
	}

	emailList := make([]map[string]interface{}, 0, len(emails))
	for _, email := range emails {
		emailList = append(emailList, map[string]interface{}{
			"uid":     email.UID,
			"subject": email.Subject,
			"from":    email.From,
			"to":      email.To,
			"date":    email.Date,
			"size":    email.Size,
			"flags":   email.Flags,
		})
	}

	totalPages := (totalCount + limit - 1) / limit

	response := map[string]interface{}{
		"emails":      emailList,
		"page":        page,
		"limit":       limit,
		"total":       totalCount,
		"total_pages": totalPages,
	}

	s.writeJSON(w, response)
}

func (s *Server) getEmail(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mailbox := vars["name"]
	uidStr := vars["uid"]

	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid UID", http.StatusBadRequest)
		return
	}

	email, err := s.storage.GetEmail(mailbox, uint32(uid))
	if err != nil {
		s.log.WithError(err).Error("Failed to get email")
		http.Error(w, "Failed to get email", http.StatusInternalServerError)
		return
	}

	if email == nil {
		http.Error(w, "Email not found", http.StatusNotFound)
		return
	}

	bodyText, bodyHTML := s.parseEmailBody(email.RawMessage)
	body := bodyHTML
	if body == "" {
		body = bodyText
	}

	response := map[string]interface{}{
		"uid":      email.UID,
		"mailbox":  email.Mailbox,
		"subject":  email.Subject,
		"from":     email.From,
		"to":       email.To,
		"date":     email.Date,
		"size":     email.Size,
		"flags":    email.Flags,
		"body":     body,
		"bodyText": bodyText,
		"bodyHTML": bodyHTML,
		"synced":   email.Synced,
	}

	s.writeJSON(w, response)
}

func (s *Server) parseEmailBody(rawMessage []byte) (textBody, htmlBody string) {
	if len(rawMessage) == 0 {
		return "", ""
	}

	msg, err := mail.ReadMessage(bytes.NewReader(rawMessage))
	if err != nil {
		s.log.WithError(err).Error("Failed to parse email")
		return string(rawMessage), ""
	}

	contentType := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "text/plain"
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		textBody, htmlBody = s.parseMultipart(msg.Body, params["boundary"])
		return textBody, htmlBody
	}

	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return "", ""
	}

	decoded := s.decodeBody(body, msg.Header.Get("Content-Transfer-Encoding"))

	if strings.HasPrefix(mediaType, "text/html") {
		return "", string(decoded)
	}

	return string(decoded), ""
}

func (s *Server) parseMultipart(body io.Reader, boundary string) (textBody, htmlBody string) {
	mr := multipart.NewReader(body, boundary)

	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}

		contentType := part.Header.Get("Content-Type")
		mediaType, params, _ := mime.ParseMediaType(contentType)

		if strings.HasPrefix(mediaType, "multipart/") {
			text, html := s.parseMultipart(part, params["boundary"])
			if textBody == "" {
				textBody = text
			}
			if htmlBody == "" {
				htmlBody = html
			}
			continue
		}

		partBody, err := io.ReadAll(part)
		if err != nil {
			continue
		}

		decoded := s.decodeBody(partBody, part.Header.Get("Content-Transfer-Encoding"))

		if strings.HasPrefix(mediaType, "text/plain") && textBody == "" {
			textBody = string(decoded)
		} else if strings.HasPrefix(mediaType, "text/html") && htmlBody == "" {
			htmlBody = string(decoded)
		}
	}

	return textBody, htmlBody
}

func (s *Server) decodeBody(body []byte, encoding string) []byte {
	encoding = strings.ToLower(strings.TrimSpace(encoding))

	switch encoding {
	case "quoted-printable":
		decoder := quotedprintable.NewReader(bytes.NewReader(body))
		decoded, err := io.ReadAll(decoder)
		if err != nil {
			return body
		}
		return decoded

	case "base64":
		decoded := make([]byte, base64.StdEncoding.DecodedLen(len(body)))
		n, err := base64.StdEncoding.Decode(decoded, body)
		if err != nil {
			return body
		}
		return decoded[:n]

	default:
		return body
	}
}

func (s *Server) downloadEmail(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mailbox := vars["name"]
	uidStr := vars["uid"]

	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid UID", http.StatusBadRequest)
		return
	}

	email, err := s.storage.GetEmail(mailbox, uint32(uid))
	if err != nil {
		s.log.WithError(err).Error("Failed to get email")
		http.Error(w, "Failed to get email", http.StatusInternalServerError)
		return
	}

	if email == nil {
		http.Error(w, "Email not found", http.StatusNotFound)
		return
	}

	if len(email.RawMessage) == 0 {
		http.Error(w, "Raw message not available", http.StatusNotFound)
		return
	}

	filename := fmt.Sprintf("%s_%d.eml", mailbox, uid)
	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(email.RawMessage)))

	w.Write(email.RawMessage)
}

func (s *Server) serveUI(w http.ResponseWriter, _ *http.Request) {
	html := s.getUIHTML()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func (s *Server) writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.log.WithError(err).Error("Failed to encode JSON")
	}
}

func (s *Server) getUIHTML() string {
	return strings.TrimSpace(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Email Browser</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: #f5f5f5;
        }
        .container { display: flex; height: 100vh; }
        .sidebar {
            width: 250px;
            background: #2c3e50;
            color: white;
            overflow-y: auto;
        }
        .sidebar h2 {
            padding: 20px;
            background: #1a252f;
            font-size: 18px;
        }
        .mailbox-item {
            padding: 12px 20px;
            cursor: pointer;
            border-bottom: 1px solid #34495e;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .mailbox-item:hover { background: #34495e; }
        .mailbox-item.active { background: #3498db; }
        .mailbox-name {
            flex: 1;
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
        }
        .mailbox-count {
            background: #1a252f;
            padding: 2px 8px;
            border-radius: 10px;
            font-size: 11px;
            font-weight: 600;
            margin-left: 8px;
        }
        .mailbox-item.active .mailbox-count {
            background: #2980b9;
        }
        .email-list {
            width: 350px;
            background: white;
            border-right: 1px solid #ddd;
            overflow-y: auto;
            display: flex;
            flex-direction: column;
        }
        .email-list h2 {
            padding: 20px;
            background: #ecf0f1;
            font-size: 16px;
            border-bottom: 1px solid #ddd;
        }
        .email-list-content {
            flex: 1;
            overflow-y: auto;
        }
        .pagination {
            display: flex;
            justify-content: center;
            align-items: center;
            padding: 10px;
            background: #ecf0f1;
            border-top: 1px solid #ddd;
            gap: 10px;
        }
        .pagination button {
            padding: 5px 10px;
            background: #3498db;
            color: white;
            border: none;
            border-radius: 3px;
            cursor: pointer;
            font-size: 12px;
        }
        .pagination button:disabled {
            background: #95a5a6;
            cursor: not-allowed;
        }
        .pagination button:hover:not(:disabled) {
            background: #2980b9;
        }
        .pagination span {
            font-size: 12px;
            color: #555;
        }
        .email-item {
            padding: 15px;
            border-bottom: 1px solid #eee;
            cursor: pointer;
        }
        .email-item:hover { background: #f8f9fa; }
        .email-item.active { background: #e3f2fd; }
        .email-subject {
            font-weight: 600;
            margin-bottom: 5px;
            font-size: 14px;
        }
        .email-from {
            font-size: 12px;
            color: #666;
            margin-bottom: 3px;
        }
        .email-date {
            font-size: 11px;
            color: #999;
        }
        .email-viewer {
            flex: 1;
            background: white;
            overflow-y: auto;
            padding: 20px;
        }
        .email-header {
            border-bottom: 2px solid #eee;
            padding-bottom: 15px;
            margin-bottom: 20px;
        }
        .email-header-top {
            display: flex;
            justify-content: space-between;
            align-items: flex-start;
            margin-bottom: 10px;
        }
        .email-header h1 {
            font-size: 24px;
            margin: 0;
            flex: 1;
        }
        .download-btn {
            display: inline-block;
            padding: 8px 16px;
            background: #3498db;
            color: white;
            text-decoration: none;
            border-radius: 4px;
            font-size: 14px;
            font-weight: 500;
            transition: background 0.2s;
            white-space: nowrap;
            margin-left: 20px;
        }
        .download-btn:hover {
            background: #2980b9;
        }
        .email-meta {
            font-size: 13px;
            color: #666;
            line-height: 1.6;
        }
        .email-body {
            white-space: pre-wrap;
            font-family: monospace;
            font-size: 13px;
            line-height: 1.5;
        }
        .empty-state {
            display: flex;
            align-items: center;
            justify-content: center;
            height: 100%;
            color: #999;
            font-size: 14px;
        }
        .loading { text-align: center; padding: 20px; color: #666; }
    </style>
</head>
<body>
    <div class="container">
        <div class="sidebar">
            <h2>Mailboxes</h2>
            <div id="mailboxes"></div>
        </div>
        <div class="email-list">
            <h2 id="list-title">Select a mailbox</h2>
            <div class="email-list-content" id="emails"></div>
            <div class="pagination" id="pagination" style="display: none;">
                <button id="first-page" onclick="goToPage(1)">First</button>
                <button id="prev-page" onclick="goToPage(currentPage - 1)">Previous</button>
                <span id="page-info">Page 1 of 1</span>
                <button id="next-page" onclick="goToPage(currentPage + 1)">Next</button>
                <button id="last-page" onclick="goToPage(totalPages)">Last</button>
            </div>
        </div>
        <div class="email-viewer">
            <div class="empty-state">Select an email to view</div>
        </div>
    </div>

    <script>
        let currentMailbox = null;
        let currentEmail = null;
        let currentPage = 1;
        let totalPages = 1;
        let pageLimit = 50;

        async function loadMailboxes() {
            const res = await fetch('/api/v1/mailboxes');
            const mailboxes = await res.json();

            const container = document.getElementById('mailboxes');
            container.innerHTML = mailboxes.map(mb => ` + "`" + `
                <div class="mailbox-item" data-mailbox="${escapeHtml(mb.name)}">
                    <div class="mailbox-name">${escapeHtml(mb.name)}</div>
                    <div class="mailbox-count">${mb.count || 0}</div>
                </div>
            ` + "`" + `).join('');

            document.querySelectorAll('.mailbox-item').forEach(el => {
                el.addEventListener('click', () => {
                    loadEmails(el.dataset.mailbox, 1);
                });
            });
        }

        async function loadEmails(mailbox, page = 1) {
            currentMailbox = mailbox;
            currentPage = page;
            document.getElementById('list-title').textContent = mailbox;

            document.querySelectorAll('.mailbox-item').forEach(el => {
                el.classList.remove('active');
                const nameEl = el.querySelector('.mailbox-name');
                if (nameEl && nameEl.textContent.trim() === mailbox) {
                    el.classList.add('active');
                }
            });

            const container = document.getElementById('emails');
            container.innerHTML = '<div class="loading">Loading...</div>';

            const res = await fetch(` + "`" + `/api/v1/mailboxes/${encodeURIComponent(mailbox)}/emails?page=${page}&limit=${pageLimit}` + "`" + `);
            const data = await res.json();

            if (!data.emails || data.emails.length === 0) {
                container.innerHTML = '<div class="loading">No emails</div>';
                document.getElementById('pagination').style.display = 'none';
                return;
            }

            totalPages = data.total_pages || 1;
            updatePagination();

            container.innerHTML = data.emails.map(email => ` + "`" + `
                <div class="email-item" data-mailbox="${escapeHtml(mailbox)}" data-uid="${email.uid}">
                    <div class="email-subject">${escapeHtml(email.subject || '(No Subject)')}</div>
                    <div class="email-from">${escapeHtml(email.from || '(Unknown)')}</div>
                    <div class="email-date">${new Date(email.date).toLocaleString()}</div>
                </div>
            ` + "`" + `).join('');

            document.querySelectorAll('.email-item').forEach(el => {
                el.addEventListener('click', function() {
                    loadEmail(this.dataset.mailbox, parseInt(this.dataset.uid));
                });
            });

            if (totalPages > 1) {
                document.getElementById('pagination').style.display = 'flex';
            } else {
                document.getElementById('pagination').style.display = 'none';
            }
        }

        function goToPage(page) {
            if (page < 1 || page > totalPages || !currentMailbox) return;
            loadEmails(currentMailbox, page);
        }

        function updatePagination() {
            document.getElementById('page-info').textContent = ` + "`" + `Page ${currentPage} of ${totalPages}` + "`" + `;
            document.getElementById('first-page').disabled = currentPage === 1;
            document.getElementById('prev-page').disabled = currentPage === 1;
            document.getElementById('next-page').disabled = currentPage === totalPages;
            document.getElementById('last-page').disabled = currentPage === totalPages;
        }

        async function loadEmail(mailbox, uid) {
            document.querySelectorAll('.email-item').forEach(el => {
                el.classList.remove('active');
                if (el.dataset.mailbox === mailbox && parseInt(el.dataset.uid) === uid) {
                    el.classList.add('active');
                }
            });

            const res = await fetch(` + "`" + `/api/v1/mailboxes/${encodeURIComponent(mailbox)}/emails/${uid}` + "`" + `);
            const email = await res.json();

            const viewer = document.querySelector('.email-viewer');
            viewer.innerHTML = ` + "`" + `
                <div class="email-header">
                    <div class="email-header-top">
                        <h1>${escapeHtml(email.subject || '(No Subject)')}</h1>
                        <a href="/api/v1/mailboxes/${encodeURIComponent(mailbox)}/emails/${uid}/download"
                           class="download-btn"
                           download="${escapeHtml(mailbox)}_${uid}.eml">
                            Download EML
                        </a>
                    </div>
                    <div class="email-meta">
                        <div><strong>From:</strong> ${escapeHtml(email.from)}</div>
                        <div><strong>To:</strong> ${escapeHtml(email.to.join(', '))}</div>
                        <div><strong>Date:</strong> ${new Date(email.date).toLocaleString()}</div>
                        <div><strong>Size:</strong> ${email.size} bytes</div>
                    </div>
                </div>
                <div class="email-body" id="email-body-content"></div>
            ` + "`" + `;

            renderEmailBody(email.body);
        }

        function renderEmailBody(body) {
            const container = document.getElementById('email-body-content');

            if (isHtmlContent(body)) {
                const iframe = document.createElement('iframe');
                iframe.style.width = '100%';
                iframe.style.border = 'none';
                iframe.style.minHeight = '400px';
                container.appendChild(iframe);

                const doc = iframe.contentDocument || iframe.contentWindow.document;
                doc.open();
                doc.write(body);
                doc.close();

                iframe.onload = () => {
                    iframe.style.height = (iframe.contentWindow.document.body.scrollHeight + 20) + 'px';
                };
            } else {
                const pre = document.createElement('pre');
                pre.style.whiteSpace = 'pre-wrap';
                pre.style.wordWrap = 'break-word';
                pre.style.fontFamily = 'monospace';
                pre.style.padding = '10px';
                pre.textContent = body;
                container.appendChild(pre);
            }
        }

        function isHtmlContent(content) {
            if (!content) return false;
            const htmlPattern = /<(?:html|body|div|p|table|br|span|a|img|h[1-6])[>\s]/i;
            return htmlPattern.test(content);
        }

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }

        loadMailboxes();
    </script>
</body>
</html>
`)
}

func (s *Server) Run(addr string) error {
	s.log.Infof("Starting email browser server on http://%s", addr)
	return http.ListenAndServe(addr, s)
}
