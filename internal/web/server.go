package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"cube20/internal/manager"
)

type Server struct {
	Manager *manager.Manager
	Host    string
	Port    int
}

type pageData struct {
	GeneratedAt string
}

func (s *Server) ListenAndServe() error {
	if s.Host == "" {
		s.Host = "127.0.0.1"
	}
	if s.Port == 0 {
		s.Port = 8720
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/accounts/import-json", s.handleImportJSON)
	mux.HandleFunc("/api/accounts", s.handleAccounts)
	mux.HandleFunc("/api/accounts/", s.handleAccountAction)
	mux.HandleFunc("/api/meta", s.handleMeta)

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	fmt.Printf("cube dashboard: http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := pageData{GeneratedAt: time.Now().Format(time.RFC3339)}
	_ = indexTemplate.Execute(w, data)
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"statePath":   s.Manager.StatePath,
		"accountsDir": s.Manager.AccountsDir,
	})
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		accounts, err := s.Manager.ListAccounts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, accounts)
	case http.MethodPost:
		var body struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		account, err := s.Manager.AddAccount(body.ID, body.Label)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, s.ManagerAccountView(account))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read json")
		return
	}

	profile, err := parseProfileUpload(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	account, err := s.Manager.ImportJSONProfile(profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.ManagerAccountView(account))
}

func (s *Server) handleAccountAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := parts[1]

	switch action {
	case "status":
		if r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Status manager.AccountStatus `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.Manager.SetStatus(id, body.Status); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": string(body.Status)})
	case "deploy":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		target, err := s.Manager.DeployAuth(id, "")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"target": target})
	case "quota":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		result, err := s.Manager.FetchQuota(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "usage":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		result, err := s.Manager.FetchUsage(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) ManagerAccountView(account manager.Account) manager.AccountView {
	views, err := s.Manager.ListAccounts()
	if err != nil {
		return manager.AccountView{Account: account}
	}
	for _, view := range views {
		if view.ID == account.ID {
			return view
		}
	}
	return manager.AccountView{Account: account}
}

func parseProfileUpload(raw []byte) (manager.JSONProfile, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return manager.JSONProfile{}, fmt.Errorf("invalid JSON: %w", err)
	}

	if authRaw, ok := root["auth"]; ok {
		profile := manager.JSONProfile{Auth: authRaw}
		profile.ID = rawString(root["id"])
		profile.Label = rawString(root["label"])
		profile.Config = rawString(root["config"])
		if profile.Config == "" {
			profile.Config = rawString(root["configToml"])
		}
		if profile.Config == "" {
			profile.Config = rawString(root["config_toml"])
		}
		return profile, nil
	}

	return manager.JSONProfile{
		ID:    rawString(root["id"]),
		Label: rawString(root["label"]),
		Auth:  json.RawMessage(raw),
	}, nil
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

var indexTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>cube20</title>
  <style>
    :root {
      color-scheme: light;
      --ink: #1c2430;
      --muted: #5c6675;
      --line: #d9dee7;
      --panel: #ffffff;
      --page: #f6f7f9;
      --accent: #197278;
      --accent-2: #8b5e34;
      --danger: #b42318;
      --warn: #b26a00;
      --ok: #146c43;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--page);
      color: var(--ink);
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      font-size: 14px;
      letter-spacing: 0;
    }
    button, input, select {
      font: inherit;
    }
    button {
      border: 1px solid var(--line);
      background: #fff;
      color: var(--ink);
      min-height: 34px;
      padding: 0 12px;
      border-radius: 6px;
      cursor: pointer;
    }
    button:hover { border-color: var(--accent); }
    button.primary {
      background: var(--accent);
      border-color: var(--accent);
      color: #fff;
    }
    button.danger {
      color: var(--danger);
    }
    input, select {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 6px;
      min-height: 34px;
      padding: 0 10px;
      background: #fff;
      color: var(--ink);
    }
    header {
      height: 56px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 24px;
      background: #fbfbfc;
      border-bottom: 1px solid var(--line);
    }
    h1 {
      margin: 0;
      font-size: 18px;
      font-weight: 700;
    }
    main {
      display: grid;
      grid-template-columns: minmax(0, 1fr) 340px;
      gap: 18px;
      padding: 18px 24px 24px;
    }
    .toolbar {
      display: flex;
      gap: 10px;
      align-items: center;
      color: var(--muted);
    }
    .surface {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      overflow: hidden;
    }
    .surface-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      padding: 14px 16px;
      border-bottom: 1px solid var(--line);
    }
    .surface-head h2 {
      margin: 0;
      font-size: 15px;
      font-weight: 700;
    }
    table {
      width: 100%;
      border-collapse: collapse;
    }
    th, td {
      padding: 12px 16px;
      text-align: left;
      border-bottom: 1px solid var(--line);
      vertical-align: middle;
      white-space: nowrap;
    }
    th {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      font-weight: 700;
    }
    tr:last-child td { border-bottom: 0; }
    .mono {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 12px;
    }
    .path {
      max-width: 340px;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .badge {
      display: inline-flex;
      align-items: center;
      min-height: 24px;
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 0 9px;
      font-size: 12px;
      font-weight: 700;
    }
    .ready { color: var(--ok); border-color: #a9d6bf; background: #eef8f2; }
    .drain { color: var(--warn); border-color: #f0c978; background: #fff6df; }
    .disabled { color: var(--danger); border-color: #efa8a1; background: #fff1f0; }
    .missing { color: var(--danger); }
    .quota-cell {
      display: flex;
      gap: 6px;
      flex-wrap: wrap;
      min-width: 160px;
    }
    .quota-pill {
      border: 1px solid var(--line);
      border-radius: 6px;
      min-height: 24px;
      padding: 2px 7px;
      font-size: 12px;
      font-weight: 700;
      background: #fff;
    }
    .quota-warn { color: var(--warn); border-color: #f0c978; background: #fff6df; }
    .quota-crit { color: var(--danger); border-color: #efa8a1; background: #fff1f0; }
    .usage-cell {
      display: grid;
      gap: 2px;
      color: var(--muted);
      min-width: 132px;
    }
    .usage-cell strong {
      color: var(--ink);
      font-weight: 700;
    }
    .side {
      display: grid;
      gap: 18px;
    }
    form {
      display: grid;
      gap: 12px;
      padding: 16px;
    }
    label {
      display: grid;
      gap: 6px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 700;
      text-transform: uppercase;
    }
    .form-row {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 10px;
    }
    .actions {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }
    .message {
      min-height: 22px;
      color: var(--muted);
      padding: 0 16px 16px;
    }
    .meta {
      display: grid;
      gap: 8px;
      padding: 16px;
      color: var(--muted);
    }
    .empty {
      padding: 28px 16px;
      color: var(--muted);
      text-align: center;
    }
    @media (max-width: 900px) {
      main {
        grid-template-columns: 1fr;
        padding: 14px;
      }
      header {
        padding: 0 14px;
      }
      .path {
        max-width: 220px;
      }
    }
  </style>
</head>
<body>
  <header>
    <h1>cube20</h1>
    <div class="toolbar">
      <span id="summary">0 accounts</span>
      <button id="refresh" title="Refresh accounts">Refresh</button>
    </div>
  </header>
  <main>
    <section class="surface">
      <div class="surface-head">
        <h2>Accounts</h2>
        <div class="toolbar">
          <span id="statePath" class="mono"></span>
        </div>
      </div>
      <div id="tableMount"></div>
    </section>
    <aside class="side">
      <section class="surface">
        <div class="surface-head">
          <h2>Add Account</h2>
        </div>
        <form id="addForm">
          <label for="accountId">ID<input id="accountId" autocomplete="off" placeholder="work-plus"></label>
          <label for="accountLabel">Label<input id="accountLabel" autocomplete="off" placeholder="Plus seat"></label>
          <button class="primary" type="submit">Add</button>
          <label for="profileJson">Import JSON<input id="profileJson" type="file" accept=".json,application/json"></label>
        </form>
        <div id="addMessage" class="message"></div>
      </section>
      <section class="surface">
        <div class="surface-head">
          <h2>Selected</h2>
        </div>
        <form id="actionForm">
          <label for="selectedAccount">Account<select id="selectedAccount"></select></label>
          <div class="form-row">
            <label for="statusSelect">Status<select id="statusSelect">
              <option value="ready">ready</option>
              <option value="drain">drain</option>
              <option value="disabled">disabled</option>
            </select></label>
            <label for="authState">Auth<input id="authState" disabled></label>
          </div>
          <label for="configState">Config<input id="configState" disabled></label>
          <div class="actions">
            <button type="button" id="saveStatus">Save Status</button>
            <button type="button" id="refreshQuota">Refresh Quota</button>
            <button type="button" id="refreshUsage">Refresh Usage</button>
            <button type="button" id="deployAuth" class="danger">Deploy Profile</button>
          </div>
        </form>
        <div id="actionMessage" class="message"></div>
      </section>
      <section class="surface">
        <div class="surface-head">
          <h2>Local Paths</h2>
        </div>
        <div class="meta">
          <div><strong>State</strong></div>
          <div id="metaState" class="mono"></div>
          <div><strong>Homes</strong></div>
          <div id="metaHomes" class="mono"></div>
        </div>
      </section>
    </aside>
  </main>
  <script>
    let accounts = [];
    let quotas = {};
    let usages = {};

    async function request(path, options) {
      const response = await fetch(path, options);
      const data = await response.json();
      if (!response.ok) {
        throw new Error(data.error || "request failed");
      }
      return data;
    }

    async function load() {
      const meta = await request("/api/meta");
      accounts = await request("/api/accounts");
      document.getElementById("metaState").textContent = meta.statePath;
      document.getElementById("metaHomes").textContent = meta.accountsDir;
      document.getElementById("statePath").textContent = meta.statePath;
      document.getElementById("summary").textContent = accounts.length + (accounts.length === 1 ? " account" : " accounts");
      renderTable();
      renderSelector();
    }

    function renderTable() {
      const mount = document.getElementById("tableMount");
      if (accounts.length === 0) {
        mount.innerHTML = '<div class="empty">No accounts</div>';
        return;
      }
      const rows = accounts.map(account => {
        const auth = account.authPresent ? "ready" : "missing";
        const config = account.configPresent ? "ready" : "missing";
        const quota = quotas[account.id];
        const usage = usages[account.id];
        return '<tr>' +
          '<td class="mono">' + escapeHTML(account.id) + '</td>' +
          '<td><span class="badge ' + escapeHTML(account.status) + '">' + escapeHTML(account.status) + '</span></td>' +
          '<td class="' + auth + '">' + auth + '</td>' +
          '<td class="' + config + '">' + config + '</td>' +
          '<td>' + escapeHTML(account.plan || "-") + '</td>' +
          '<td>' + quotaMarkup(quota) + '</td>' +
          '<td>' + usageMarkup(usage) + '</td>' +
          '<td class="mono path" title="' + escapeHTML(account.codexHome) + '">' + escapeHTML(account.codexHome) + '</td>' +
        '</tr>';
      }).join("");
      mount.innerHTML = '<table><thead><tr><th>ID</th><th>Status</th><th>Auth</th><th>Config</th><th>Plan</th><th>Quota</th><th>Local Usage</th><th>CODEX_HOME</th></tr></thead><tbody>' + rows + '</tbody></table>';
    }

    function renderSelector() {
      const select = document.getElementById("selectedAccount");
      const previous = select.value;
      select.innerHTML = accounts.map(account => '<option value="' + escapeHTML(account.id) + '">' + escapeHTML(account.id) + '</option>').join("");
      if (accounts.some(account => account.id === previous)) {
        select.value = previous;
      }
      syncSelected();
    }

    function syncSelected() {
      const id = document.getElementById("selectedAccount").value;
      const account = accounts.find(item => item.id === id);
      document.getElementById("statusSelect").value = account ? account.status : "ready";
      document.getElementById("authState").value = account && account.authPresent ? "ready" : "missing";
      document.getElementById("configState").value = account && account.configPresent ? "ready" : "missing";
    }

    function quotaMarkup(quota) {
      if (!quota) return '<span class="missing">not loaded</span>';
      if (quota.status !== "supported") return '<span class="missing">' + escapeHTML(quota.status || "error") + '</span>';
      if (!Array.isArray(quota.quotas) || quota.quotas.length === 0) return '<span>no quota</span>';
      return '<div class="quota-cell">' + quota.quotas.map(item => {
        const used = Number(item.usedPercent || 0);
        const sev = used >= 90 ? ' quota-crit' : used >= 75 ? ' quota-warn' : '';
        const reset = item.resetsAt ? ' title="resets ' + escapeHTML(item.resetsAt) + '"' : '';
        return '<span class="quota-pill' + sev + '"' + reset + '>' + escapeHTML(item.label) + ' ' + escapeHTML(item.usedDisplay || Math.round(used) + "%") + '</span>';
      }).join("") + '</div>';
    }

    function usageMarkup(usage) {
      if (!usage) return '<span class="missing">not loaded</span>';
      if (usage.status !== "ok") return '<span class="missing">' + escapeHTML(usage.status || "error") + '</span>';
      const today = usage.today && Number(usage.today.total || 0);
      const seven = usage.sevenDays && Number(usage.sevenDays.total || 0);
      return '<div class="usage-cell"><strong>Today ' + formatTokens(today) + '</strong><span>7d ' + formatTokens(seven) + '</span></div>';
    }

    function formatTokens(value) {
      const n = Number(value || 0);
      if (n >= 1000000000) return (n / 1000000000).toFixed(1) + 'B';
      if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
      if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
      return String(Math.round(n));
    }

    function escapeHTML(value) {
      return String(value || "").replace(/[&<>"']/g, char => ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#039;"
      }[char]));
    }

    document.getElementById("refresh").addEventListener("click", () => load().catch(showActionError));
    document.getElementById("selectedAccount").addEventListener("change", syncSelected);

    document.getElementById("addForm").addEventListener("submit", async event => {
      event.preventDefault();
      const id = document.getElementById("accountId").value.trim();
      const label = document.getElementById("accountLabel").value.trim();
      try {
        await request("/api/accounts", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ id, label })
        });
        document.getElementById("accountId").value = "";
        document.getElementById("accountLabel").value = "";
        document.getElementById("addMessage").textContent = "Account added";
        await load();
      } catch (error) {
        document.getElementById("addMessage").textContent = error.message;
      }
    });

    document.getElementById("profileJson").addEventListener("change", async event => {
      const file = event.target.files && event.target.files[0];
      if (!file) return;
      try {
        const text = await file.text();
        const account = await request("/api/accounts/import-json", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: text
        });
        event.target.value = "";
        document.getElementById("addMessage").textContent = "Imported " + account.id;
        await load();
      } catch (error) {
        event.target.value = "";
        document.getElementById("addMessage").textContent = error.message;
      }
    });

    document.getElementById("saveStatus").addEventListener("click", async () => {
      const id = document.getElementById("selectedAccount").value;
      const status = document.getElementById("statusSelect").value;
      if (!id) return;
      try {
        await request("/api/accounts/" + encodeURIComponent(id) + "/status", {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ status })
        });
        document.getElementById("actionMessage").textContent = "Status updated";
        await load();
      } catch (error) {
        showActionError(error);
      }
    });

    document.getElementById("deployAuth").addEventListener("click", async () => {
      const id = document.getElementById("selectedAccount").value;
      if (!id) return;
      try {
        const result = await request("/api/accounts/" + encodeURIComponent(id) + "/deploy", { method: "POST" });
        document.getElementById("actionMessage").textContent = "Deployed to " + result.target;
        await load();
      } catch (error) {
        showActionError(error);
      }
    });

    document.getElementById("refreshQuota").addEventListener("click", async () => {
      const id = document.getElementById("selectedAccount").value;
      if (!id) return;
      try {
        const result = await request("/api/accounts/" + encodeURIComponent(id) + "/quota");
        quotas[id] = result;
        document.getElementById("actionMessage").textContent = quotaMessage(result);
        renderTable();
      } catch (error) {
        showActionError(error);
      }
    });

    document.getElementById("refreshUsage").addEventListener("click", async () => {
      const id = document.getElementById("selectedAccount").value;
      if (!id) return;
      try {
        const result = await request("/api/accounts/" + encodeURIComponent(id) + "/usage");
        usages[id] = result;
        document.getElementById("actionMessage").textContent = usageMessage(result);
        renderTable();
      } catch (error) {
        showActionError(error);
      }
    });

    function quotaMessage(result) {
      if (!result || result.status !== "supported") return result && result.detail ? result.detail : "Quota unavailable";
      const parts = (result.quotas || []).map(q => q.label + " " + (q.usedDisplay || Math.round(q.usedPercent || 0) + "%"));
      return "Quota refreshed" + (result.plan ? " (" + result.plan + ")" : "") + ": " + parts.join(", ");
    }

    function usageMessage(result) {
      if (!result) return "Usage unavailable";
      return "Usage refreshed: today " + formatTokens(result.today && result.today.total) + ", 7d " + formatTokens(result.sevenDays && result.sevenDays.total);
    }

    function showActionError(error) {
      document.getElementById("actionMessage").textContent = error.message;
    }

    load().catch(showActionError);
  </script>
</body>
</html>`))
