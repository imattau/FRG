package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultListenAddr = "127.0.0.1:8080"
	defaultFRGAddr    = "127.0.0.1:50051"
	requestTimeout    = 15 * time.Second
)

var pageTemplate = template.Must(template.New("page").Parse(`
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>FRG Web Client</title>
  <style>
    :root {
      color-scheme: dark;
      --bg: #0b1020;
      --panel: rgba(16, 22, 44, 0.88);
      --panel-strong: #111936;
      --border: rgba(126, 146, 255, 0.18);
      --text: #ecf1ff;
      --muted: #9aa7cf;
      --accent: #7cf2c8;
      --accent-2: #8aa6ff;
      --danger: #ff7f93;
      --shadow: 0 24px 80px rgba(0, 0, 0, 0.35);
      --mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      --sans: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: var(--sans);
      background:
        radial-gradient(circle at top left, rgba(124, 242, 200, 0.12), transparent 26%),
        radial-gradient(circle at top right, rgba(138, 166, 255, 0.18), transparent 30%),
        linear-gradient(180deg, #09101d 0%, #0b1020 100%);
      color: var(--text);
    }
    .shell {
      max-width: 1180px;
      margin: 0 auto;
      padding: 32px 20px 48px;
    }
    header {
      display: flex;
      justify-content: space-between;
      align-items: flex-end;
      gap: 20px;
      margin-bottom: 24px;
    }
    h1 {
      margin: 0;
      font-size: clamp(2rem, 5vw, 3.8rem);
      letter-spacing: -0.04em;
      line-height: 0.95;
    }
    .subtitle {
      margin-top: 10px;
      max-width: 64ch;
      color: var(--muted);
      font-size: 0.98rem;
    }
    .badge {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      padding: 8px 12px;
      border: 1px solid var(--border);
      border-radius: 999px;
      background: rgba(255, 255, 255, 0.04);
      color: var(--muted);
      font-family: var(--mono);
      font-size: 0.86rem;
    }
    .grid {
      display: grid;
      grid-template-columns: 1.1fr 0.9fr;
      gap: 18px;
    }
    .card {
      background: var(--panel);
      border: 1px solid var(--border);
      box-shadow: var(--shadow);
      border-radius: 20px;
      padding: 18px;
      backdrop-filter: blur(14px);
    }
    .card h2 {
      margin: 0 0 14px;
      font-size: 1rem;
      letter-spacing: 0.02em;
      text-transform: uppercase;
      color: var(--muted);
    }
    .field {
      display: grid;
      gap: 8px;
      margin-bottom: 14px;
    }
    label {
      font-size: 0.88rem;
      color: var(--muted);
    }
    input, textarea, button {
      font: inherit;
    }
    input, textarea {
      width: 100%;
      border-radius: 14px;
      border: 1px solid rgba(138, 166, 255, 0.22);
      background: rgba(4, 8, 20, 0.7);
      color: var(--text);
      padding: 12px 14px;
      outline: none;
    }
    textarea {
      min-height: 160px;
      resize: vertical;
      font-family: var(--mono);
      font-size: 0.88rem;
      line-height: 1.45;
    }
    input:focus, textarea:focus {
      border-color: rgba(124, 242, 200, 0.58);
      box-shadow: 0 0 0 3px rgba(124, 242, 200, 0.08);
    }
    .row {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 12px;
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      margin-top: 8px;
    }
    button {
      border: 1px solid rgba(124, 242, 200, 0.25);
      background: linear-gradient(135deg, rgba(124, 242, 200, 0.18), rgba(138, 166, 255, 0.2));
      color: var(--text);
      padding: 11px 14px;
      border-radius: 14px;
      cursor: pointer;
      transition: transform 120ms ease, border-color 120ms ease;
    }
    button:hover { transform: translateY(-1px); border-color: rgba(124, 242, 200, 0.5); }
    .secondary {
      background: rgba(255, 255, 255, 0.04);
      border-color: rgba(255, 255, 255, 0.12);
    }
    .status {
      margin-top: 10px;
      padding: 12px 14px;
      border-radius: 14px;
      background: rgba(7, 12, 28, 0.72);
      border: 1px solid rgba(255, 255, 255, 0.08);
      color: var(--muted);
      min-height: 48px;
      white-space: pre-wrap;
    }
    .status.ok { color: #9cf3d4; }
    .status.err { color: var(--danger); }
    .events {
      display: grid;
      gap: 10px;
      max-height: 540px;
      overflow: auto;
      padding-right: 2px;
    }
    .stats {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 12px;
    }
    .stat {
      border-radius: 16px;
      padding: 14px;
      border: 1px solid rgba(255,255,255,0.08);
      background: rgba(4, 8, 20, 0.66);
    }
    .stat .label {
      display: block;
      margin-bottom: 8px;
      color: var(--muted);
      font-size: 0.82rem;
      letter-spacing: 0.04em;
      text-transform: uppercase;
    }
    .stat .value {
      font-family: var(--mono);
      font-size: 1.05rem;
      word-break: break-all;
    }
    .stat .sub {
      margin-top: 8px;
      color: var(--muted);
      font-size: 0.85rem;
      line-height: 1.35;
    }
    .event {
      border-radius: 16px;
      border: 1px solid rgba(255,255,255,0.08);
      background: rgba(4, 8, 20, 0.65);
      padding: 12px 14px;
      font-family: var(--mono);
      font-size: 0.88rem;
      word-break: break-all;
    }
    .event small {
      display: block;
      margin-bottom: 6px;
      color: var(--muted);
      font-family: var(--sans);
    }
    .hint {
      color: var(--muted);
      font-size: 0.88rem;
      margin-top: -4px;
      margin-bottom: 12px;
    }
    @media (max-width: 960px) {
      .grid, .row { grid-template-columns: 1fr; }
      header { flex-direction: column; align-items: flex-start; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <header>
      <div>
        <h1>FRG Web Client</h1>
        <div class="subtitle">Submit raw FRG transactions, stream block headers, and point the dashboard at any gRPC server that implements <span style="font-family: var(--mono)">frg.FRG</span>.</div>
      </div>
      <div class="badge">gRPC endpoint: <span id="endpointBadge">{{.DefaultAddr}}</span></div>
    </header>

    <div class="grid">
      <section class="card">
        <h2>Target Server</h2>
        <div class="field">
          <label for="addr">FRG gRPC address</label>
          <input id="addr" value="{{.DefaultAddr}}" placeholder="127.0.0.1:50051">
        </div>

        <h2>Submit Tx</h2>
        <div class="field">
          <label for="txHex">Serialized tx hex</label>
          <textarea id="txHex" placeholder="Paste a hex-encoded serialized tx here"></textarea>
        </div>
        <div class="actions">
          <button id="submitTxBtn">Submit Transaction</button>
          <button id="startStreamBtn" class="secondary">Start Block Stream</button>
          <button id="stopStreamBtn" class="secondary">Stop Stream</button>
        </div>
        <div class="status" id="submitStatus">Ready.</div>

        <h2 style="margin-top: 20px;">Submit Batch</h2>
        <div class="hint">One hex-encoded serialized tx per line.</div>
        <div class="field">
          <label for="batchHex">Batch payloads</label>
          <textarea id="batchHex" placeholder="tx1 hex&#10;tx2 hex&#10;tx3 hex"></textarea>
        </div>
        <div class="actions">
          <button id="submitBatchBtn">Submit Batch</button>
        </div>
        <div class="status" id="batchStatus">No batch submitted yet.</div>
      </section>

      <section class="card">
        <h2>Live Blocks</h2>
        <div class="hint">Each event is the raw block-header payload returned by the server, shown as hex.</div>
        <div class="events" id="events"></div>
      </section>

      <section class="card">
        <h2>Node Status</h2>
        <div class="hint">This panel polls the gRPC admin API so you can see live node state even when no blocks finalize.</div>
        <div class="stats">
          <div class="stat">
            <span class="label">Height</span>
            <div class="value" id="statusHeight">-</div>
            <div class="sub" id="statusRound">Round: -</div>
          </div>
          <div class="stat">
            <span class="label">Consensus</span>
            <div class="value" id="statusPhase">-</div>
            <div class="sub" id="statusGrpcOnly">grpc-only: -</div>
          </div>
          <div class="stat">
            <span class="label">Peers</span>
            <div class="value" id="statusPeers">-</div>
            <div class="sub">Connected libp2p peers</div>
          </div>
          <div class="stat">
            <span class="label">Mempool</span>
            <div class="value" id="statusMempool">-</div>
            <div class="sub">Queued transactions waiting for proposal</div>
          </div>
          <div class="stat">
            <span class="label">Validators</span>
            <div class="value" id="statusValidators">-</div>
            <div class="sub">Bonded validator count</div>
          </div>
          <div class="stat">
            <span class="label">State Root</span>
            <div class="value" id="statusRoot">-</div>
            <div class="sub">Last committed state root</div>
          </div>
        </div>
        <div class="status" id="statusPanel">Waiting for node status...</div>
      </section>
    </div>
  </div>

  <script>
    const addrEl = document.getElementById('addr');
    const endpointBadge = document.getElementById('endpointBadge');
    const submitStatus = document.getElementById('submitStatus');
    const batchStatus = document.getElementById('batchStatus');
    const events = document.getElementById('events');
    const txHex = document.getElementById('txHex');
    const batchHex = document.getElementById('batchHex');
    const statusHeight = document.getElementById('statusHeight');
    const statusRound = document.getElementById('statusRound');
    const statusPhase = document.getElementById('statusPhase');
    const statusGrpcOnly = document.getElementById('statusGrpcOnly');
    const statusPeers = document.getElementById('statusPeers');
    const statusMempool = document.getElementById('statusMempool');
    const statusValidators = document.getElementById('statusValidators');
    const statusRoot = document.getElementById('statusRoot');
    const statusPanel = document.getElementById('statusPanel');
    const submitTxBtn = document.getElementById('submitTxBtn');
    const submitBatchBtn = document.getElementById('submitBatchBtn');
    const startStreamBtn = document.getElementById('startStreamBtn');
    const stopStreamBtn = document.getElementById('stopStreamBtn');

    let source = null;
    let statusTimer = null;

    function setStatus(el, msg, kind) {
      el.className = 'status' + (kind ? ' ' + kind : '');
      el.textContent = msg;
    }

    function normalizeHex(s) {
      return s.trim().replace(/^0x/i, '').replace(/\s+/g, '');
    }

    function updateEndpointBadge() {
      endpointBadge.textContent = addrEl.value.trim() || '{{.DefaultAddr}}';
    }

    function addEvent(title, payload) {
      const box = document.createElement('div');
      box.className = 'event';
      box.innerHTML = '<small>' + title + '</small>' + payload;
      events.prepend(box);
    }

    function shortRoot(hexValue) {
      if (!hexValue) return '-';
      return hexValue.length > 20 ? hexValue.slice(0, 20) + '…' : hexValue;
    }

    function renderStatus(status) {
      statusHeight.textContent = status.height ?? 0;
      statusRound.textContent = 'Round: ' + (status.consensus_round ?? 0);
      statusPhase.textContent = (status.consensus_phase || 'unknown').toUpperCase();
      statusGrpcOnly.textContent = 'grpc-only: ' + (status.grpc_only ? 'yes' : 'no');
      statusPeers.textContent = status.peer_count ?? 0;
      statusMempool.textContent = status.mempool_len ?? 0;
      statusValidators.textContent = status.validator_count ?? 0;
      statusRoot.textContent = shortRoot(status.state_root_hex || '');

      const details = [];
      if (status.grpc_only) {
        details.push('grpc-only mode keeps consensus idle');
      }
      if ((status.mempool_len ?? 0) === 0) {
        details.push('mempool is empty');
      }
      if ((status.consensus_phase || '').toLowerCase() !== 'commit') {
        details.push('consensus is currently in ' + (status.consensus_phase || 'unknown') + ' phase');
      }

      statusPanel.textContent = details.length ? details.join(' | ') : 'Node is actively building blocks.';
      statusPanel.className = 'status' + (status.grpc_only ? ' ok' : '');
    }

    async function postJSON(path, body) {
      const res = await fetch(path + '?addr=' + encodeURIComponent(addrEl.value.trim() || '{{.DefaultAddr}}'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      return await res.json();
    }

    async function fetchStatus() {
      try {
        const res = await fetch('/api/status?addr=' + encodeURIComponent(addrEl.value.trim() || '{{.DefaultAddr}}'));
        const status = await res.json();
        renderStatus(status);
      } catch (err) {
        statusPanel.textContent = 'Status fetch failed: ' + String(err);
        statusPanel.className = 'status err';
      }
    }

    function startStatusPolling() {
      if (statusTimer) {
        clearInterval(statusTimer);
      }
      fetchStatus();
      statusTimer = setInterval(fetchStatus, 2000);
    }

    submitTxBtn.addEventListener('click', async () => {
      updateEndpointBadge();
      try {
        const txHexValue = normalizeHex(txHex.value);
        const resp = await postJSON('/api/submit-tx', { tx_hex: txHexValue });
        if (resp.ok) {
          setStatus(submitStatus, 'Transaction submitted successfully.', 'ok');
        } else {
          setStatus(submitStatus, resp.error || 'Transaction rejected.', 'err');
        }
      } catch (err) {
        setStatus(submitStatus, String(err), 'err');
      }
    });

    submitBatchBtn.addEventListener('click', async () => {
      updateEndpointBadge();
      try {
        const payloads = batchHex.value.split('\n').map(normalizeHex).filter(Boolean);
        const resp = await postJSON('/api/submit-batch', { tx_hexes: payloads });
        if (resp.ok) {
          setStatus(batchStatus, 'Batch submitted successfully.', 'ok');
        } else {
          setStatus(batchStatus, resp.error || 'Batch rejected.', 'err');
        }
      } catch (err) {
        setStatus(batchStatus, String(err), 'err');
      }
    });

    startStreamBtn.addEventListener('click', () => {
      updateEndpointBadge();
      if (source) {
        source.close();
      }
      const addr = encodeURIComponent(addrEl.value.trim() || '{{.DefaultAddr}}');
      source = new EventSource('/api/blocks?addr=' + addr);
      source.onmessage = (evt) => {
        try {
          const block = JSON.parse(evt.data);
          addEvent('block ' + block.size + ' bytes', block.hex);
        } catch (err) {
          addEvent('block', evt.data);
        }
      };
      source.onerror = () => {
        addEvent('stream', 'connection closed or interrupted');
      };
      setStatus(submitStatus, 'Streaming blocks from ' + (addrEl.value.trim() || '{{.DefaultAddr}}') + '.', 'ok');
    });

    stopStreamBtn.addEventListener('click', () => {
      if (source) {
        source.close();
        source = null;
      }
      setStatus(submitStatus, 'Block stream stopped.', '');
    });

    addrEl.addEventListener('change', () => {
      updateEndpointBadge();
      startStatusPolling();
    });

    updateEndpointBadge();
    startStatusPolling();
  </script>
</body>
</html>
`))

type server struct {
	defaultAddr string
	dial        func(context.Context, string) (*grpc.ClientConn, error)
}

type submitTxRequest struct {
	TxHex string `json:"tx_hex"`
}

type submitBatchRequest struct {
	TxHexes []string `json:"tx_hexes"`
}

func main() {
	listenAddr := flag.String("listen", defaultListenAddr, "address for the web client to listen on")
	frgAddr := flag.String("frg-addr", defaultFRGAddr, "default FRG gRPC address")
	flag.Parse()

	srv := &server{
		defaultAddr: *frgAddr,
		dial:        dialGRPC,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/api/submit-tx", srv.handleSubmitTx)
	mux.HandleFunc("/api/submit-batch", srv.handleSubmitBatch)
	mux.HandleFunc("/api/blocks", srv.handleBlocks)
	mux.HandleFunc("/api/status", srv.handleStatus)

	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("FRG web client listening on http://%s", *listenAddr)
	log.Printf("Default FRG server: %s", *frgAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := pageTemplate.Execute(w, map[string]string{"DefaultAddr": s.defaultAddr}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) handleSubmitTx(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr := addrFromRequest(r, s.defaultAddr)
	defer r.Body.Close()

	var req submitTxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body: " + err.Error()})
		return
	}

	raw, err := decodeHexBytes(req.TxHex)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	resp, err := submitTx(r.Context(), s.dial, addr, raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleSubmitBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr := addrFromRequest(r, s.defaultAddr)
	defer r.Body.Close()

	var req submitBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body: " + err.Error()})
		return
	}

	rawBatch := make([][]byte, 0, len(req.TxHexes))
	for _, h := range req.TxHexes {
		raw, err := decodeHexBytes(h)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		rawBatch = append(rawBatch, raw)
	}

	resp, err := submitBatch(r.Context(), s.dial, addr, rawBatch)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr := addrFromRequest(r, s.defaultAddr)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	conn, err := s.dial(r.Context(), addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	stream, err := client.SubscribeBlocks(r.Context(), &frgpb.Empty{}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		default:
		}

		msg, err := stream.Recv()
		if err != nil {
			return
		}
		payload, _ := json.Marshal(map[string]any{
			"hex":  hex.EncodeToString(msg.Data),
			"size": len(msg.Data),
		})
		if _, err := io.WriteString(w, "data: "+string(payload)+"\n\n"); err != nil {
			return
		}
		flusher.Flush()
	}
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr := addrFromRequest(r, s.defaultAddr)

	conn, err := s.dial(r.Context(), addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetStatus(r.Context(), &frgpb.Empty{}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"height":          resp.Height,
		"state_root_hex":  hex.EncodeToString(resp.StateRoot),
		"peer_count":      resp.PeerCount,
		"mempool_len":     resp.MempoolLen,
		"validator_count": resp.ValidatorCount,
		"consensus_round": resp.ConsensusRound,
		"consensus_phase": resp.ConsensusPhase,
		"grpc_only":       resp.GrpcOnly,
	})
}

func submitTx(ctx context.Context, dial func(context.Context, string) (*grpc.ClientConn, error), addr string, raw []byte) (map[string]any, error) {
	conn, err := dialWithTimeout(ctx, dial, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.SubmitTx(ctx, &frgpb.RawBytes{Data: raw}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": resp.Ok, "error": resp.Error}, nil
}

func submitBatch(ctx context.Context, dial func(context.Context, string) (*grpc.ClientConn, error), addr string, raw [][]byte) (map[string]any, error) {
	conn, err := dialWithTimeout(ctx, dial, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.SubmitBatch(ctx, &frgpb.RawBytesArray{Data: raw}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": resp.Ok, "error": resp.Error}, nil
}

func dialWithTimeout(ctx context.Context, dial func(context.Context, string) (*grpc.ClientConn, error), addr string) (*grpc.ClientConn, error) {
	cctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return dial(cctx, addr)
}

func dialGRPC(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	return grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype("frg-json")),
	)
}

func addrFromRequest(r *http.Request, fallback string) string {
	if v := strings.TrimSpace(r.URL.Query().Get("addr")); v != "" {
		return v
	}
	return fallback
}

func decodeHexBytes(s string) ([]byte, error) {
	cleaned := strings.TrimSpace(s)
	cleaned = strings.TrimPrefix(cleaned, "0x")
	cleaned = strings.TrimPrefix(cleaned, "0X")
	if cleaned == "" {
		return nil, fmt.Errorf("hex payload is empty")
	}
	raw, err := hex.DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("decode hex: %w", err)
	}
	return raw, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
