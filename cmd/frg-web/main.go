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
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imattau/frg/core/denom"
	frgpb "github.com/imattau/frg/proto"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultListenAddr = "127.0.0.1:8080"
	defaultFRGAddr    = "127.0.0.1:50051"
	defaultDBPath     = "explorer.db"
	requestTimeout    = 15 * time.Second
	maxBlockHistory   = 1000
	maxWebRequestBody = 8 << 20
	maxWebBatchItems  = 1024
)

var pageTemplate = template.Must(template.New("page").Parse(`
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>FRG Devnet Explorer</title>
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
    .shell { max-width: 1180px; margin: 0 auto; padding: 32px 20px 48px; }
    header { display: flex; justify-content: space-between; align-items: flex-end; gap: 20px; margin-bottom: 24px; }
    h1 { margin: 0; font-size: clamp(2rem, 5vw, 3.8rem); letter-spacing: -0.04em; line-height: 0.95; }
    .subtitle { margin-top: 10px; max-width: 64ch; color: var(--muted); font-size: 0.98rem; }
    .badge { display: inline-flex; align-items: center; gap: 8px; padding: 8px 12px; border: 1px solid var(--border); border-radius: 999px; background: rgba(255, 255, 255, 0.04); color: var(--muted); font-family: var(--mono); font-size: 0.86rem; }
    .tabs { display: flex; gap: 4px; margin-bottom: 18px; }
    .tab { padding: 8px 18px; border: 1px solid var(--border); border-radius: 12px 12px 0 0; background: rgba(255, 255, 255, 0.03); color: var(--muted); cursor: pointer; font-size: 0.9rem; transition: background 120ms; }
    .tab.active { background: var(--panel); color: var(--text); border-bottom-color: transparent; }
    .tab:hover { background: rgba(255, 255, 255, 0.06); }
    .grid { display: grid; grid-template-columns: 1.1fr 0.9fr; gap: 18px; }
    .grid3 { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 18px; }
    .card { background: var(--panel); border: 1px solid var(--border); box-shadow: var(--shadow); border-radius: 20px; padding: 18px; backdrop-filter: blur(14px); }
    .card h2 { margin: 0 0 14px; font-size: 1rem; letter-spacing: 0.02em; text-transform: uppercase; color: var(--muted); }
    .field { display: grid; gap: 8px; margin-bottom: 14px; }
    label { font-size: 0.88rem; color: var(--muted); }
    input, textarea, button { font: inherit; }
    input, textarea {
      width: 100%; border-radius: 14px; border: 1px solid rgba(138, 166, 255, 0.22);
      background: rgba(4, 8, 20, 0.7); color: var(--text); padding: 12px 14px; outline: none;
    }
    textarea { min-height: 160px; resize: vertical; font-family: var(--mono); font-size: 0.88rem; line-height: 1.45; }
    input:focus, textarea:focus { border-color: rgba(124, 242, 200, 0.58); box-shadow: 0 0 0 3px rgba(124, 242, 200, 0.08); }
    .row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
    .actions { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 8px; }
    button {
      border: 1px solid rgba(124, 242, 200, 0.25);
      background: linear-gradient(135deg, rgba(124, 242, 200, 0.18), rgba(138, 166, 255, 0.2));
      color: var(--text); padding: 11px 14px; border-radius: 14px; cursor: pointer;
      transition: transform 120ms ease, border-color 120ms ease;
    }
    button:hover { transform: translateY(-1px); border-color: rgba(124, 242, 200, 0.5); }
    .secondary { background: rgba(255, 255, 255, 0.04); border-color: rgba(255, 255, 255, 0.12); }
    .status { margin-top: 10px; padding: 12px 14px; border-radius: 14px; background: rgba(7, 12, 28, 0.72); border: 1px solid rgba(255, 255, 255, 0.08); color: var(--muted); min-height: 48px; white-space: pre-wrap; }
    .status.ok { color: #9cf3d4; }
    .status.err { color: var(--danger); }
    .events { display: grid; gap: 10px; max-height: 540px; overflow: auto; padding-right: 2px; }
    .stats { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
    .stat { border-radius: 16px; padding: 14px; border: 1px solid rgba(255,255,255,0.08); background: rgba(4, 8, 20, 0.66); }
    .stat .label { display: block; margin-bottom: 8px; color: var(--muted); font-size: 0.82rem; letter-spacing: 0.04em; text-transform: uppercase; }
    .stat .value { font-family: var(--mono); font-size: 1.05rem; word-break: break-all; }
    .stat .sub { margin-top: 8px; color: var(--muted); font-size: 0.85rem; line-height: 1.35; }
    .event { border-radius: 16px; border: 1px solid rgba(255,255,255,0.08); background: rgba(4, 8, 20, 0.65); padding: 12px 14px; font-family: var(--mono); font-size: 0.88rem; word-break: break-all; }
    .event small { display: block; margin-bottom: 6px; color: var(--muted); font-family: var(--sans); }
    .hint { color: var(--muted); font-size: 0.88rem; margin-top: -4px; margin-bottom: 12px; }
    .vtable { width: 100%; border-collapse: collapse; font-family: var(--mono); font-size: 0.85rem; }
    .vtable th { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--border); color: var(--muted); text-transform: uppercase; font-size: 0.78rem; }
    .vtable td { padding: 8px 10px; border-bottom: 1px solid rgba(255,255,255,0.05); word-break: break-all; }
    .mempool-item { border-radius: 14px; border: 1px solid rgba(255,255,255,0.07); background: rgba(4, 8, 20, 0.55); padding: 10px 14px; margin-bottom: 6px; font-family: var(--mono); font-size: 0.85rem; }
    .mempool-item .mpid { color: var(--muted); font-size: 0.8rem; }
    .block-row { border-radius: 14px; border: 1px solid rgba(255,255,255,0.07); background: rgba(4, 8, 20, 0.55); padding: 10px 14px; margin-bottom: 6px; font-family: var(--mono); font-size: 0.85rem; cursor: pointer; transition: background 120ms; }
    .block-row:hover { background: rgba(124, 242, 200, 0.06); }
    .block-row .bh { color: var(--accent); }
    .empty-state { text-align: center; color: var(--muted); padding: 24px; }
    @media (max-width: 960px) { .grid, .row, .grid3 { grid-template-columns: 1fr; } header { flex-direction: column; align-items: flex-start; } }
  </style>
</head>
<body>
  <div class="shell">
    <header>
      <div>
        <h1>FRG Devnet Explorer</h1>
        <div class="subtitle">Submit transactions, view validators, monitor mempool, and track block production on any FRG node.</div>
      </div>
      <div class="badge">gRPC: <span id="endpointBadge">{{.DefaultAddr}}</span></div>
    </header>

    <div class="tabs">
      <div class="tab active" data-tab="submit">Submit Tx</div>
      <div class="tab" data-tab="network">Network</div>
      <div class="tab" data-tab="validators">Validators</div>
      <div class="tab" data-tab="mempool">Mempool</div>
      <div class="tab" data-tab="blocks">Blocks</div>
      <div class="tab" data-tab="account">Account</div>
    </div>

    <div id="tab-submit">
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
          <div class="hint">Streaming block-header payloads from the node, shown as hex.</div>
          <div class="events" id="events"></div>
        </section>
        <section class="card">
          <h2>Node Status</h2>
          <div class="stats">
            <div class="stat"><span class="label">Height</span><div class="value" id="statusHeight">-</div><div class="sub" id="statusRound">Round: -</div></div>
            <div class="stat"><span class="label">Consensus</span><div class="value" id="statusPhase">-</div><div class="sub" id="statusGrpcOnly">grpc-only: -</div></div>
            <div class="stat"><span class="label">Peers</span><div class="value" id="statusPeers">-</div><div class="sub">Connected libp2p peers</div></div>
            <div class="stat"><span class="label">Mempool</span><div class="value" id="statusMempool">-</div><div class="sub">Queued transactions</div></div>
            <div class="stat"><span class="label">Validators</span><div class="value" id="statusValidators">-</div><div class="sub">Bonded validators</div></div>
            <div class="stat"><span class="label">State Root</span><div class="value" id="statusRoot">-</div><div class="sub">Last committed</div></div>
          </div>
          <div class="status" id="statusPanel">Waiting for node status...</div>
        </section>
      </div>
    </div>

    <div id="tab-network" style="display:none">
      <div class="grid3">
        <div class="card"><h2>Node Info</h2><div id="netNodeInfo" class="empty-state">Loading...</div></div>
        <div class="card"><h2>Consensus</h2><div id="netConsensus" class="empty-state">Loading...</div></div>
        <div class="card"><h2>State Root</h2><div id="netRoot" class="empty-state" style="font-family:var(--mono);font-size:0.82rem;">Loading...</div></div>
      </div>
    </div>

    <div id="tab-validators" style="display:none">
      <div class="grid">
        <section class="card" style="grid-column: 1/-1">
          <h2>Bonded Validators <span id="valCount" style="font-weight:normal;color:var(--muted)">(-)</span></h2>
          <div class="hint" id="valTotalBond" style="margin-bottom:8px;">Total stake: --</div>
          <table class="vtable"><thead><tr><th>#</th><th>Pubkey</th><th>Bond</th></tr></thead><tbody id="valTable"><tr><td colspan="3" class="empty-state">Loading validators...</td></tr></tbody></table>
        </section>
      </div>
    </div>

    <div id="tab-mempool" style="display:none">
      <div class="grid">
        <section class="card" style="grid-column: 1/-1">
          <h2>Pending Transactions <span id="mpCount" style="font-weight:normal;color:var(--muted)">(-)</span></h2>
          <div class="hint">Transactions waiting to be included in a block.</div>
          <div id="mpList"><div class="empty-state">Loading mempool...</div></div>
        </section>
      </div>
    </div>

    <div id="tab-blocks" style="display:none">
      <div class="grid">
        <section class="card" style="grid-column: 1/-1">
          <h2>Block History <span id="blkCount" style="font-weight:normal;color:var(--muted)">(-)</span></h2>
          <div class="hint">Recently committed blocks. Newest first.</div>
          <div id="blkList"><div class="empty-state">No blocks recorded yet. Start the block stream on the "Submit Tx" tab.</div></div>
        </section>
      </div>
    </div>

    <div id="tab-account" style="display:none">
      <div class="grid">
        <section class="card">
          <h2>Account Lookup</h2>
          <div class="field">
            <label for="acctPubkey">Pubkey (hex)</label>
            <input id="acctPubkey" placeholder="32-byte Ed25519 pubkey hex">
          </div>
          <div class="actions"><button id="acctLookupBtn">Lookup</button></div>
          <div id="acctResult" class="status">Enter a pubkey to query balance and nonce.</div>
        </section>
      </div>
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

    let source = null, statusTimer = null, validatorsTimer = null, mempoolTimer = null, blocksTimer = null;

    function nodeAddr() { return addrEl.value.trim() || '{{.DefaultAddr}}'; }
    function setStatus(el, msg, kind) { el.className = 'status' + (kind ? ' ' + kind : ''); el.textContent = msg; }
    function normalizeHex(s) { return s.trim().replace(/^0x/i, '').replace(/\s+/g, ''); }
    function updateEndpointBadge() { endpointBadge.textContent = nodeAddr(); }
    function addEvent(title, payload) {
      const box = document.createElement('div'); box.className = 'event';
      box.innerHTML = '<small>' + title + '</small>' + payload;
      events.prepend(box);
    }
    function shortHex(h, n) { n = n || 12; if (!h) return '-'; return h.length > n*2 ? h.slice(0, n) + '…' : h; }

    // Tab switching
    document.querySelectorAll('.tab').forEach(t => {
      t.addEventListener('click', () => {
        document.querySelectorAll('.tab').forEach(x => x.classList.remove('active'));
        t.classList.add('active');
        const id = 'tab-' + t.dataset.tab;
        document.querySelectorAll('[id^="tab-"]').forEach(x => x.style.display = 'none');
        document.getElementById(id).style.display = '';
        if (t.dataset.tab === 'validators') fetchValidators();
        if (t.dataset.tab === 'mempool') fetchMempool();
        if (t.dataset.tab === 'blocks') fetchBlocks();
      });
    });

    function renderStatus(status) {
      statusHeight.textContent = status.height ?? 0;
      statusRound.textContent = 'Round: ' + (status.consensus_round ?? 0);
      statusPhase.textContent = (status.consensus_phase || 'unknown').toUpperCase();
      statusGrpcOnly.textContent = 'grpc-only: ' + (status.grpc_only ? 'yes' : 'no');
      statusPeers.textContent = status.peer_count ?? 0;
      statusMempool.textContent = status.mempool_len ?? 0;
      statusValidators.textContent = status.validator_count ?? 0;
      statusRoot.textContent = shortHex(status.state_root_hex || '', 10);
      const details = [];
      if (status.grpc_only) details.push('grpc-only mode keeps consensus idle');
      if ((status.mempool_len ?? 0) === 0) details.push('mempool is empty');
      if ((status.consensus_phase || '').toLowerCase() !== 'commit') details.push('consensus is in ' + (status.consensus_phase || 'unknown') + ' phase');
      statusPanel.textContent = details.length ? details.join(' | ') : 'Node is actively building blocks.';
      statusPanel.className = 'status' + (status.grpc_only ? ' ok' : '');
      document.getElementById('netNodeInfo').innerHTML = 'Height: <b>' + (status.height ?? 0) + '</b><br>Peers: <b>' + (status.peer_count ?? 0) + '</b><br>Mempool: <b>' + (status.mempool_len ?? 0) + '</b>';
      document.getElementById('netConsensus').innerHTML = 'Phase: <b>' + (status.consensus_phase || 'unknown').toUpperCase() + '</b><br>Round: <b>' + (status.consensus_round ?? 0) + '</b><br>Validators: <b>' + (status.validator_count ?? 0) + '</b>';
      document.getElementById('netRoot').textContent = '0x' + (status.state_root_hex || '');
    }

    async function postJSON(path, body) {
      const res = await fetch(path + '?addr=' + encodeURIComponent(nodeAddr()), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      return await res.json();
    }
    async function fetchJSON(path) {
      const res = await fetch(path + '?addr=' + encodeURIComponent(nodeAddr()));
      return await res.json();
    }
    async function fetchStatus() {
      try {
        const status = await fetchJSON('/api/status');
        renderStatus(status);
      } catch (err) { statusPanel.textContent = 'Status fetch failed: ' + String(err); statusPanel.className = 'status err'; }
    }
    function startStatusPolling() {
      if (statusTimer) clearInterval(statusTimer);
      fetchStatus();
      statusTimer = setInterval(fetchStatus, 2000);
    }

    // Validators panel
    async function fetchValidators() {
      try {
        const data = await fetchJSON('/api/validators');
        const tbody = document.getElementById('valTable');
        document.getElementById('valCount').textContent = '(' + (data.validators ? data.validators.length : 0) + ')';
        document.getElementById('valTotalBond').textContent = 'Total stake: ' + (data.total_bond_frg || '0') + ' FRG';
        if (!data.validators || data.validators.length === 0) { tbody.innerHTML = '<tr><td colspan="3" class="empty-state">No validators bonded.</td></tr>'; return; }
        tbody.innerHTML = data.validators.map((v, i) => '<tr><td>' + (i+1) + '</td><td>' + shortHex(v.pubkey_hex, 14) + '</td><td>' + (v.bond_frg || '0') + ' FRG</td></tr>').join('');
      } catch(e) { document.getElementById('valTable').innerHTML = '<tr><td colspan="3" class="empty-state">Error: ' + e + '</td></tr>'; }
    }
    if (validatorsTimer) clearInterval(validatorsTimer);
    validatorsTimer = setInterval(() => { if (document.getElementById('tab-validators').style.display !== 'none') fetchValidators(); }, 5000);

    // Mempool panel
    async function fetchMempool() {
      try {
        const data = await fetchJSON('/api/mempool');
        document.getElementById('mpCount').textContent = '(' + (data.entries ? data.entries.length : 0) + ')';
        const list = document.getElementById('mpList');
        if (!data.entries || data.entries.length === 0) { list.innerHTML = '<div class="empty-state">Mempool is empty.</div>'; return; }
        list.innerHTML = data.entries.map(e => '<div class="mempool-item"><div><b>Sender:</b> ' + (e.sender || '-') + '</div><div class="mpid"><b>TxID:</b> ' + shortHex(e.txid_hex, 12) + ' &nbsp; <b>Nonce:</b> ' + (e.nonce || 0) + '</div></div>').join('');
      } catch(e) { document.getElementById('mpList').innerHTML = '<div class="empty-state">Error: ' + e + '</div>'; }
    }
    if (mempoolTimer) clearInterval(mempoolTimer);
    mempoolTimer = setInterval(() => { if (document.getElementById('tab-mempool').style.display !== 'none') fetchMempool(); }, 3000);

    // Blocks panel
    async function fetchBlocks() {
      try {
        const data = await fetchJSON('/api/blocks-history');
        document.getElementById('blkCount').textContent = '(' + (data.blocks ? data.blocks.length : 0) + ')';
        const list = document.getElementById('blkList');
        if (!data.blocks || data.blocks.length === 0) { list.innerHTML = '<div class="empty-state">No blocks recorded yet. Start the block stream on the "Submit Tx" tab.</div>'; return; }
        list.innerHTML = data.blocks.map(b => '<div class="block-row"><span class="bh">#' + b.height + '</span> &nbsp; rx:' + shortHex(b.state_root, 8) + ' <span style="color:var(--muted);float:right">' + b.time + '</span></div>').join('');
      } catch(e) { document.getElementById('blkList').innerHTML = '<div class="empty-state">Error: ' + e + '</div>'; }
    }
    if (blocksTimer) clearInterval(blocksTimer);
    blocksTimer = setInterval(() => { if (document.getElementById('tab-blocks').style.display !== 'none') fetchBlocks(); }, 3000);

    // Account lookup
    document.getElementById('acctLookupBtn').addEventListener('click', async () => {
      const pubkey = normalizeHex(document.getElementById('acctPubkey').value);
      if (!pubkey || pubkey.length !== 64) { document.getElementById('acctResult').textContent = 'Enter a valid 32-byte hex pubkey.'; return; }
      try {
        const data = await fetchJSON('/api/account?pubkey=' + pubkey);
        document.getElementById('acctResult').innerHTML = 'Pubkey: <b>' + shortHex(data.pubkey, 16) + '</b><br>Balance: <b>' + (data.balance_frg || '0') + ' FRG</b><br>Quanta: <b>' + (data.balance || '0') + '</b><br>Nonce: <b>' + (data.nonce || 0) + '</b>';
        document.getElementById('acctResult').className = 'status ok';
      } catch(e) { document.getElementById('acctResult').textContent = 'Error: ' + e; document.getElementById('acctResult').className = 'status err'; }
    });

    // Original submit/stream handlers
    submitTxBtn.addEventListener('click', async () => {
      updateEndpointBadge();
      try {
        const resp = await postJSON('/api/submit-tx', { tx_hex: normalizeHex(txHex.value) });
        if (resp.ok) setStatus(submitStatus, 'Transaction submitted successfully.', 'ok');
        else setStatus(submitStatus, resp.error || 'Transaction rejected.', 'err');
      } catch (err) { setStatus(submitStatus, String(err), 'err'); }
    });
    submitBatchBtn.addEventListener('click', async () => {
      updateEndpointBadge();
      try {
        const payloads = batchHex.value.split('\n').map(normalizeHex).filter(Boolean);
        const resp = await postJSON('/api/submit-batch', { tx_hexes: payloads });
        if (resp.ok) setStatus(batchStatus, 'Batch submitted successfully.', 'ok');
        else setStatus(batchStatus, resp.error || 'Batch rejected.', 'err');
      } catch (err) { setStatus(batchStatus, String(err), 'err'); }
    });
    startStreamBtn.addEventListener('click', () => {
      updateEndpointBadge();
      if (source) source.close();
      const addr = encodeURIComponent(nodeAddr());
      source = new EventSource('/api/blocks?addr=' + addr);
      source.onmessage = (evt) => {
        try { addEvent('block ' + JSON.parse(evt.data).size + ' bytes', JSON.parse(evt.data).hex); }
        catch (err) { addEvent('block', evt.data); }
      };
      source.onerror = () => addEvent('stream', 'connection closed or interrupted');
      setStatus(submitStatus, 'Streaming blocks from ' + nodeAddr() + '.', 'ok');
    });
    stopStreamBtn.addEventListener('click', () => { if (source) { source.close(); source = null; } setStatus(submitStatus, 'Block stream stopped.', ''); });
    addrEl.addEventListener('change', () => { updateEndpointBadge(); startStatusPolling(); });

    updateEndpointBadge();
    startStatusPolling();
  </script>
</body>
</html>
`))

var blocksBucket = []byte("blocks")

type server struct {
	defaultAddr string
	dial        func(context.Context, string) (*grpc.ClientConn, error)
	db          *bolt.DB
	blockMu     sync.Mutex
	blockCount  uint64
}

type submitTxRequest struct {
	TxHex string `json:"tx_hex"`
}

type submitBatchRequest struct {
	TxHexes []string `json:"tx_hexes"`
}

type blockRecord struct {
	Height    uint64 `json:"height"`
	StateRoot string `json:"state_root"`
	Time      string `json:"time"`
	Data      string `json:"data"`
}

func main() {
	listenAddr := flag.String("listen", defaultListenAddr, "address for the web client to listen on")
	frgAddr := flag.String("frg-addr", defaultFRGAddr, "default FRG gRPC address")
	dbPath := flag.String("db", defaultDBPath, "block history database path")
	flag.Parse()

	db, err := bolt.Open(*dbPath, 0600, nil)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(blocksBucket)
		return err
	}); err != nil {
		log.Fatalf("create bucket: %v", err)
	}

	srv := &server{
		defaultAddr: *frgAddr,
		dial:        dialGRPC,
		db:          db,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/api/submit-tx", srv.handleSubmitTx)
	mux.HandleFunc("/api/submit-batch", srv.handleSubmitBatch)
	mux.HandleFunc("/api/blocks", srv.handleBlocks)
	mux.HandleFunc("/api/blocks-history", srv.handleBlocksHistory)
	mux.HandleFunc("/api/status", srv.handleStatus)
	mux.HandleFunc("/api/validators", srv.handleValidators)
	mux.HandleFunc("/api/mempool", srv.handleMempool)
	mux.HandleFunc("/api/account", srv.handleAccount)

	go srv.captureBlocks()

	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
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
	addr, err := addrFromRequest(r, s.defaultAddr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebRequestBody)
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
	addr, err := addrFromRequest(r, s.defaultAddr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebRequestBody)
	defer r.Body.Close()

	var req submitBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body: " + err.Error()})
		return
	}
	if len(req.TxHexes) > maxWebBatchItems {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "batch item limit exceeded"})
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
	addr, err := addrFromRequest(r, s.defaultAddr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

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
	stream, err := client.SubscribeBlocks(r.Context(), &frgpb.Empty{})
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

func (s *server) handleBlocksHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = r.URL.Query().Get("addr")

	var blocks []blockRecord
	s.blockMu.Lock()
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(blocksBucket)
		c := b.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var br blockRecord
			if err := json.Unmarshal(v, &br); err != nil {
				continue
			}
			blocks = append(blocks, br)
			if len(blocks) >= maxBlockHistory {
				break
			}
		}
		return nil
	})
	s.blockMu.Unlock()

	if blocks == nil {
		blocks = []blockRecord{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks})
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr, err := addrFromRequest(r, s.defaultAddr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn, err := s.dial(r.Context(), addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetStatus(r.Context(), &frgpb.Empty{})
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

func (s *server) handleValidators(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr, err := addrFromRequest(r, s.defaultAddr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn, err := s.dial(r.Context(), addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.ListValidators(r.Context(), &frgpb.Empty{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	type valOut struct {
		PubkeyHex string `json:"pubkey_hex"`
		Bond      string `json:"bond"`
		BondFRG   string `json:"bond_frg"`
	}
	out := make([]valOut, 0, len(resp.Validators))
	totalBond := big.NewInt(0)
	for _, v := range resp.Validators {
		if q, ok := new(big.Int).SetString(v.Bond, 10); ok {
			totalBond.Add(totalBond, q)
		}
		out = append(out, valOut{
			PubkeyHex: hex.EncodeToString(v.Pubkey),
			Bond:      v.Bond,
			BondFRG:   formatQuantaAsFRG(v.Bond),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"validators": out, "total_bond_frg": denom.FormatFRG(totalBond), "total_bond": totalBond.String()})
}

func (s *server) handleMempool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr, err := addrFromRequest(r, s.defaultAddr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn, err := s.dial(r.Context(), addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.ListMempool(r.Context(), &frgpb.Empty{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	type mpOut struct {
		TxidHex string `json:"txid_hex"`
		Sender  string `json:"sender"`
		Nonce   uint64 `json:"nonce"`
	}
	out := make([]mpOut, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		out = append(out, mpOut{
			TxidHex: hex.EncodeToString(e.Txid),
			Sender:  e.Sender,
			Nonce:   e.Nonce,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func (s *server) handleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr, err := addrFromRequest(r, s.defaultAddr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pubkeyHex := strings.TrimSpace(r.URL.Query().Get("pubkey"))
	if pubkeyHex == "" {
		http.Error(w, "missing pubkey query param", http.StatusBadRequest)
		return
	}
	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(pubkeyBytes) != 32 {
		http.Error(w, "invalid pubkey hex (must be 32 bytes)", http.StatusBadRequest)
		return
	}

	conn, err := s.dial(r.Context(), addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetAccount(r.Context(), &frgpb.AccountRequest{Pubkey: pubkeyBytes})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pubkey":      hex.EncodeToString(resp.Pubkey),
		"balance":     resp.Balance,
		"balance_frg": formatQuantaAsFRG(resp.Balance),
		"nonce":       resp.Nonce,
	})
}

func formatQuantaAsFRG(raw string) string {
	q, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return "0"
	}
	return denom.FormatFRG(q)
}

func (s *server) captureBlocks() {
	// Periodically poll status and record blocks when height changes
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastHeight uint64
	for range ticker.C {
		conn, err := s.dial(context.Background(), s.defaultAddr)
		if err != nil {
			continue
		}
		client := frgpb.NewFRGClient(conn)
		resp, err := client.GetStatus(context.Background(), &frgpb.Empty{})
		conn.Close()
		if err != nil {
			continue
		}
		if resp.Height > lastHeight {
			lastHeight = resp.Height
			br := blockRecord{
				Height:    resp.Height,
				StateRoot: hex.EncodeToString(resp.StateRoot),
				Time:      time.Now().UTC().Format("15:04:05"),
			}
			s.recordBlock(br)
		}
	}
}

func (s *server) recordBlock(br blockRecord) {
	s.blockMu.Lock()
	defer s.blockMu.Unlock()

	data, _ := json.Marshal(br)

	_ = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(blocksBucket)
		key := fmt.Sprintf("%020d", br.Height)
		return b.Put([]byte(key), data)
	})

	s.blockCount++
	if s.blockCount > maxBlockHistory+500 {
		var toDelete int
		_ = s.db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket(blocksBucket)
			c := b.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				_ = b.Delete(k)
				toDelete++
				if toDelete >= 500 {
					break
				}
			}
			return nil
		})
		s.blockCount -= uint64(toDelete)
	}
}

func submitTx(ctx context.Context, dial func(context.Context, string) (*grpc.ClientConn, error), addr string, raw []byte) (map[string]any, error) {
	conn, err := dialWithTimeout(ctx, dial, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.SubmitTx(ctx, &frgpb.RawBytes{Data: raw})
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
	resp, err := client.SubmitBatch(ctx, &frgpb.RawBytesArray{Data: raw})
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
	)
}

func addrFromRequest(r *http.Request, fallback string) (string, error) {
	if v := strings.TrimSpace(r.URL.Query().Get("addr")); v != "" {
		if err := validateLoopbackAddr(v); err != nil {
			return "", err
		}
		return v, nil
	}
	if err := validateLoopbackAddr(fallback); err != nil {
		return "", err
	}
	return fallback, nil
}

func validateLoopbackAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("gRPC address must be loopback host:port")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("gRPC address port is invalid")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("gRPC address must be loopback")
	}
	return nil
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
