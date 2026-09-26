// Lifel — Hardware Media & Live WebSocket Bridge (Go + HTMX Companion)
(() => {
  let appConfig = {
    model: "gemini-3.8-live",
    voice_name: "Puck",
    echo_shield: true,
    voice_barge_in: true,
    barge_in_threshold: 3200,
    vision_resolution: 1080,
    mirror_camera: false,
  };

  let ws = null;
  let isConnected = false;
  let isMuted = false;

  // Audio contexts & nodes
  let micCtx = null;
  let micStream = null;
  let micWorkletNode = null;
  let playCtx = null;
  let nextPlayTime = 0;
  let activeSources = [];
  let lastSpeakerEndTime = 0;
  let speechTailFrames = 0;
  let bargeInHotFrames = 0;

  // Vision state
  let videoStream = null;
  let visionMode = null; // "webcam" | "screen" | null
  let visionInterval = null;
  let isMirrored = false;

  // Streaming transcript DOM references
  let currentUserBubble = null;
  let currentModelBubble = null;
  let activeVoiceName = "Puck";

  const $ = (id) => document.getElementById(id);

  // =========================================================================
  // 1. State Sync & HTMX Integration
  // =========================================================================
  async function syncStateFromServer() {
    try {
      const res = await fetch("/api/state");
      if (!res.ok) return;
      const data = await res.json();
      if (data.config) {
        appConfig = data.config;
        activeVoiceName = appConfig.voice_name || "Puck";
        if ($("sidebar-voice-label")) $("sidebar-voice-label").textContent = appConfig.voice_name;
        if ($("sidebar-key-label")) $("sidebar-key-label").textContent = appConfig.selected_key || "AUTO";
        if ($("echo-shield-indicator")) {
          $("echo-shield-indicator").textContent = appConfig.echo_shield
            ? `On (Barge-In > ${appConfig.barge_in_threshold || 3200})`
            : "Off";
        }
        if ($("vision-res-select") && appConfig.vision_resolution) {
          $("vision-res-select").value = String(appConfig.vision_resolution);
        }
        isMirrored = !!appConfig.mirror_camera;
        applyMirrorStyle();
      }
    } catch (_) {}
  }

  document.body.addEventListener("configUpdated", () => {
    syncStateFromServer();
  });

  document.body.addEventListener("resumeSession", () => {
    switchTab("studio");
    syncStateFromServer();
  });

  // =========================================================================
  // 2. Sidebar Tab Switching
  // =========================================================================
  const tabTitles = {
    studio: "Live Studio",
    config: "Configuration",
    voice: "Voice & Audio",
    memory: "Memory Bank",
    tools: "Tools",
    sessions: "Session History",
    keys: "API Keys",
  };

  function switchTab(tabName) {
    document.querySelectorAll(".tab-panel").forEach((el) => {
      el.classList.toggle("hidden", el.id !== `tab-${tabName}`);
    });
    document.querySelectorAll(".nav-tab").forEach((btn) => {
      const active = btn.getAttribute("data-tab") === tabName;
      btn.className = active
        ? "nav-tab text-left px-3 py-2 rounded text-[13px] font-medium bg-[#e8f0f8] text-[#1a1a19] cursor-pointer transition-colors"
        : "nav-tab text-left px-3 py-2 rounded text-[13px] text-[#555550] hover:bg-[#f4f4f0] cursor-pointer transition-colors";
    });
    if ($("topbar-title") && tabTitles[tabName]) {
      $("topbar-title").textContent = tabTitles[tabName];
    }
    if (tabName === "sessions" && window.htmx) {
      window.htmx.ajax("GET", "/htmx/sessions", { target: "#sessions-list-container", swap: "innerHTML" });
    }
    if (tabName === "memory" && window.htmx) {
      window.htmx.ajax("GET", "/htmx/memory", { target: "#memory-list-container", swap: "innerHTML" });
    }
  }

  document.querySelectorAll(".nav-tab").forEach((btn) => {
    btn.addEventListener("click", () => switchTab(btn.getAttribute("data-tab")));
  });

  // =========================================================================
  // 3. Hardware Device Enumeration
  // =========================================================================
  async function populateHardwareDevices() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.enumerateDevices) return;
    try {
      const devices = await navigator.mediaDevices.enumerateDevices();
      const micSel = $("select-mic-device");
      const spkSel = $("select-speaker-device");
      const camSel = $("select-camera-device");
      if (!micSel || !spkSel || !camSel) return;

      micSel.innerHTML = `<option value="">System Default Microphone</option>`;
      spkSel.innerHTML = `<option value="">System Default Speaker</option>`;
      camSel.innerHTML = `<option value="">System Default Camera</option>`;

      devices.forEach((d) => {
        const opt = document.createElement("option");
        opt.value = d.deviceId;
        opt.textContent = d.label || `${d.kind} (${d.deviceId.slice(0, 6)})`;
        if (d.kind === "audioinput") micSel.appendChild(opt);
        if (d.kind === "audiooutput") spkSel.appendChild(opt);
        if (d.kind === "videoinput") camSel.appendChild(opt);
      });
    } catch (_) {}
  }

  // =========================================================================
  // 4. Speaker Playback (24kHz 16-bit Mono PCM) & Interruption
  // =========================================================================
  function ensurePlayContext() {
    if (!playCtx || playCtx.state === "closed") {
      playCtx = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 24000 });
      nextPlayTime = 0;
    }
    if (playCtx.state === "suspended") {
      playCtx.resume();
    }
  }

  function isSpeakerPlaying() {
    if (!playCtx) return false;
    return activeSources.length > 0 || playCtx.currentTime < nextPlayTime - 0.02;
  }

  function updateSpeakerUI() {
    const badge = $("speaker-state-badge");
    const intBtn = $("btn-interrupt");
    const playing = isSpeakerPlaying();
    if (badge) {
      badge.textContent = playing ? "Speaking" : "Silent";
      badge.className = playing
        ? "font-mono text-[11px] px-1.5 py-0.5 rounded bg-[#e8f0f8] text-[#1f4b7a] font-medium"
        : "font-mono text-[11px] text-[#555550]";
    }
    if (intBtn) {
      intBtn.disabled = !isConnected;
    }
  }

  function enqueuePCM24k(base64Data) {
    ensurePlayContext();
    const raw = atob(base64Data);
    const byteLen = raw.length;
    const samples = Math.floor(byteLen / 2);
    if (samples === 0) return;

    const float32 = new Float32Array(samples);
    const view = new DataView(new ArrayBuffer(byteLen));
    for (let i = 0; i < byteLen; i++) {
      view.setUint8(i, raw.charCodeAt(i));
    }
    for (let i = 0; i < samples; i++) {
      float32[i] = view.getInt16(i * 2, true) / 32768.0;
    }

    const audioBuf = playCtx.createBuffer(1, samples, 24000);
    audioBuf.copyToChannel(float32, 0);

    const src = playCtx.createBufferSource();
    src.buffer = audioBuf;
    src.connect(playCtx.destination);

    const now = playCtx.currentTime;
    if (nextPlayTime < now + 0.01) {
      nextPlayTime = now + 0.02;
    }
    src.start(nextPlayTime);
    nextPlayTime += audioBuf.duration;

    activeSources.push(src);
    updateSpeakerUI();

    src.onended = () => {
      activeSources = activeSources.filter((s) => s !== src);
      lastSpeakerEndTime = performance.now();
      updateSpeakerUI();
    };
  }

  function stopAllPlayback() {
    activeSources.forEach((s) => {
      try {
        s.stop();
        s.disconnect();
      } catch (_) {}
    });
    activeSources = [];
    if (playCtx) {
      nextPlayTime = playCtx.currentTime;
    }
    lastSpeakerEndTime = performance.now();
    updateSpeakerUI();
  }

  // =========================================================================
  // 5. Microphone Capture (16kHz 16-bit Mono PCM) + Smart Voice Barge-In
  // =========================================================================
  const WORKLET_CODE = `
    class LifelMicProcessor extends AudioWorkletProcessor {
      constructor() {
        super();
        this.buffer = new Int16Array(1600); // 100ms at 16kHz
        this.offset = 0;
      }
      process(inputs) {
        const input = inputs[0];
        if (!input || !input[0]) return true;
        const chan = input[0];
        for (let i = 0; i < chan.length; i++) {
          const s = Math.max(-1, Math.min(1, chan[i]));
          this.buffer[this.offset++] = s < 0 ? s * 0x8000 : s * 0x7FFF;
          if (this.offset >= this.buffer.length) {
            const copy = new Int16Array(this.buffer);
            this.port.postMessage(copy.buffer, [copy.buffer]);
            this.offset = 0;
          }
        }
        return true;
      }
    }
    registerProcessor("lifel-mic-processor", LifelMicProcessor);
  `;

  async function startMicrophone() {
    const micDevId = $("select-mic-device")?.value;
    const audioConstraints = {
      channelCount: 1,
      sampleRate: 16000,
      echoCancellation: true,
      noiseSuppression: true,
      autoGainControl: true,
    };
    if (micDevId) {
      audioConstraints.deviceId = { exact: micDevId };
    }

    micStream = await navigator.mediaDevices.getUserMedia({ audio: audioConstraints });
    micCtx = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 16000 });
    const blob = new Blob([WORKLET_CODE], { type: "application/javascript" });
    const workletUrl = URL.createObjectURL(blob);
    await micCtx.audioWorklet.addModule(workletUrl);
    URL.revokeObjectURL(workletUrl);

    const source = micCtx.createMediaStreamSource(micStream);
    micWorkletNode = new AudioWorkletNode(micCtx, "lifel-mic-processor");

    micWorkletNode.port.onmessage = (ev) => {
      if (!isConnected || !ws || ws.readyState !== WebSocket.OPEN) return;
      if (isMuted) {
        updateMicMeter(0, "Muted");
        return;
      }

      const pcm16 = new Int16Array(ev.data);
      let sumSq = 0;
      for (let i = 0; i < pcm16.length; i++) {
        sumSq += pcm16[i] * pcm16[i];
      }
      const rms = Math.round(Math.sqrt(sumSq / pcm16.length));

      const spkActive = isSpeakerPlaying();
      const inCooldown = performance.now() - lastSpeakerEndTime < 220;
      const gateRms = 420;
      const bargeRms = Number(appConfig.barge_in_threshold ?? 3200);

      // Smart Echo Shield + Loud Voice Barge-In
      if (appConfig.echo_shield && (spkActive || inCooldown)) {
        if (appConfig.voice_barge_in !== false && rms >= bargeRms) {
          bargeInHotFrames++;
          if (bargeInHotFrames >= 2) {
            stopAllPlayback();
            bargeInHotFrames = 0;
            speechTailFrames = 6;
            sendPcmBuffer(ev.data);
            updateMicMeter(rms, "Barge-In Triggered");
            return;
          }
        } else {
          bargeInHotFrames = 0;
        }
        updateMicMeter(rms, "Echo Shield Active");
        return;
      }

      bargeInHotFrames = 0;
      if (rms >= gateRms) {
        speechTailFrames = 5;
        sendPcmBuffer(ev.data);
        updateMicMeter(rms, "Streaming Speech");
      } else if (speechTailFrames > 0) {
        speechTailFrames--;
        sendPcmBuffer(ev.data);
        updateMicMeter(rms, "Speech Tail");
      } else {
        updateMicMeter(rms, "Below Noise Gate");
      }
    };

    source.connect(micWorkletNode);
    populateHardwareDevices();
  }

  function sendPcmBuffer(arrayBuf) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const bytes = new Uint8Array(arrayBuf);
    let binary = "";
    for (let i = 0; i < bytes.byteLength; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    ws.send(JSON.stringify({ type: "audio", data: btoa(binary) }));
  }

  function updateMicMeter(rms, statusText) {
    if ($("mic-rms-val")) $("mic-rms-val").textContent = String(rms);
    if ($("mic-gate-status")) $("mic-gate-status").textContent = statusText;
    if ($("mic-level-bar")) {
      const pct = Math.min(100, Math.round((rms / 4000) * 100));
      $("mic-level-bar").style.width = `${pct}%`;
    }
  }

  function stopMicrophone() {
    if (micWorkletNode) {
      try { micWorkletNode.disconnect(); } catch (_) {}
      micWorkletNode = null;
    }
    if (micStream) {
      micStream.getTracks().forEach((t) => t.stop());
      micStream = null;
    }
    if (micCtx) {
      try { micCtx.close(); } catch (_) {}
      micCtx = null;
    }
    updateMicMeter(0, "Idle");
  }

  // =========================================================================
  // 6. Vision Input (Webcam / Screen Share / High-Res Snapshot)
  // =========================================================================
  function getTargetMaxDim() {
    const sel = parseInt($("vision-res-select")?.value || appConfig.vision_resolution || "1080", 10);
    if (sel === 640) return 640;
    if (sel === 1280) return 1280;
    return 1080;
  }

  function applyMirrorStyle() {
    const vid = $("vision-video");
    const btn = $("btn-mirror");
    if (vid) {
      vid.style.transform = isMirrored && visionMode === "webcam" ? "scaleX(-1)" : "none";
    }
    if (btn) {
      btn.className = isMirrored
        ? "px-2 py-1 rounded border border-[#1a1a19] bg-[#e8f0f8] text-[11.5px] text-[#1a1a19] font-medium cursor-pointer"
        : "px-2 py-1 rounded border border-[#d4d4ce] bg-[#f9f9f7] text-[11.5px] text-[#1a1a19] cursor-pointer";
    }
  }

  async function startVision(mode) {
    stopVision();
    try {
      if (mode === "webcam") {
        const camId = $("select-camera-device")?.value;
        const constraints = {
          video: {
            width: { ideal: 1280 },
            height: { ideal: 720 },
            ...(camId ? { deviceId: { exact: camId } } : {}),
          },
          audio: false,
        };
        videoStream = await navigator.mediaDevices.getUserMedia(constraints);
      } else {
        videoStream = await navigator.mediaDevices.getDisplayMedia({
          video: { width: { ideal: 1920 }, height: { ideal: 1080 } },
          audio: false,
        });
      }

      visionMode = mode;
      const vid = $("vision-video");
      const ph = $("vision-placeholder");
      if (vid) {
        vid.srcObject = videoStream;
        vid.classList.remove("hidden");
      }
      if (ph) ph.classList.add("hidden");
      applyMirrorStyle();

      videoStream.getVideoTracks()[0].addEventListener("ended", () => stopVision());

      if ($("vision-status-label")) {
        $("vision-status-label").textContent = `Streaming ${mode} (1 FPS, ${getTargetMaxDim()}p)`;
      }
      if ($("btn-snap-hd")) $("btn-snap-hd").disabled = false;

      visionInterval = setInterval(() => {
        captureAndSendFrame(false);
      }, 1000);
    } catch (err) {
      if ($("vision-status-label")) {
        $("vision-status-label").textContent = `Vision error: ${err.message || err}`;
      }
    }
  }

  function captureAndSendFrame(highQuality = false) {
    if (!videoStream || !ws || ws.readyState !== WebSocket.OPEN) return;
    const vid = $("vision-video");
    const canvas = $("vision-canvas");
    if (!vid || !canvas || !vid.videoWidth) return;

    const maxDim = highQuality ? 1280 : getTargetMaxDim();
    let w = vid.videoWidth;
    let h = vid.videoHeight;
    if (w > maxDim || h > maxDim) {
      if (w >= h) {
        h = Math.round((h * maxDim) / w);
        w = maxDim;
      } else {
        w = Math.round((w * maxDim) / h);
        h = maxDim;
      }
    }

    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext("2d");
    if (isMirrored && visionMode === "webcam") {
      ctx.translate(w, 0);
      ctx.scale(-1, 1);
    }
    ctx.drawImage(vid, 0, 0, w, h);

    const quality = highQuality ? 0.92 : 0.80;
    const dataUrl = canvas.toDataURL("image/jpeg", quality);
    const b64 = dataUrl.split(",")[1];
    if (b64) {
      ws.send(JSON.stringify({ type: "image", data: b64, mime_type: "image/jpeg" }));
      if (highQuality && $("vision-status-label")) {
        $("vision-status-label").textContent = `Sent HD frame (${w}x${h})`;
      }
    }
  }

  function stopVision() {
    if (visionInterval) {
      clearInterval(visionInterval);
      visionInterval = null;
    }
    if (videoStream) {
      videoStream.getTracks().forEach((t) => t.stop());
      videoStream = null;
    }
    visionMode = null;
    const vid = $("vision-video");
    const ph = $("vision-placeholder");
    if (vid) {
      vid.srcObject = null;
      vid.classList.add("hidden");
    }
    if (ph) ph.classList.remove("hidden");
    if ($("vision-status-label")) $("vision-status-label").textContent = "No active video stream";
    if ($("btn-snap-hd")) $("btn-snap-hd").disabled = true;
  }

  // =========================================================================
  // 7. Transcript Feed & Collapsible Tool Cards
  // =========================================================================
  function hideEmptyBanner() {
    const empty = $("transcript-empty");
    if (empty) empty.classList.add("hidden");
  }

  function scrollTranscriptToBottom() {
    const feed = $("transcript-feed");
    if (feed) feed.scrollTop = feed.scrollHeight;
  }

  function nowTimeStr() {
    return new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  }

  function appendSystemNotice(text, isError = false) {
    hideEmptyBanner();
    const feed = $("transcript-feed");
    if (!feed) return;
    const div = document.createElement("div");
    div.className = isError
      ? "px-3 py-2 rounded border border-[#f2cbc6] bg-[#fae8e6] text-[#8a261d] text-[12px]"
      : "px-3 py-1.5 rounded border border-[#e4e4df] bg-white text-[#666660] text-[12px]";
    div.textContent = text;
    feed.appendChild(div);
    scrollTranscriptToBottom();
  }

  function updateOrCreateBubble(role, fullText, isFinal = false) {
    hideEmptyBanner();
    const feed = $("transcript-feed");
    if (!feed) return;

    if (role === "user") {
      if (!currentUserBubble) {
        currentUserBubble = createBubbleElement("user");
        feed.appendChild(currentUserBubble.root);
      }
      currentUserBubble.body.textContent = fullText;
      if (isFinal) currentUserBubble = null;
    } else {
      if (currentUserBubble) currentUserBubble = null;
      if (!currentModelBubble) {
        currentModelBubble = createBubbleElement("gemini");
        feed.appendChild(currentModelBubble.root);
      }
      currentModelBubble.body.textContent = fullText;
      if (isFinal) currentModelBubble = null;
    }
    scrollTranscriptToBottom();
  }

  function createBubbleElement(role) {
    const root = document.createElement("div");
    const isUser = role === "user";
    root.className = isUser
      ? "p-3 rounded-md border border-[#c9d9eb] bg-[#e8f0f8]/70 flex flex-col gap-1"
      : "p-3 rounded-md border border-[#e4e4df] bg-white flex flex-col gap-1";

    const header = document.createElement("div");
    header.className = "flex items-center justify-between text-[11px]";

    const title = document.createElement("span");
    title.className = isUser
      ? "font-semibold uppercase tracking-wider text-[#1f4b7a]"
      : "font-semibold uppercase tracking-wider text-[#1a1a19]";
    title.textContent = isUser ? "You" : `Gemini (${activeVoiceName})`;

    const meta = document.createElement("div");
    meta.className = "flex items-center gap-1.5 font-mono text-[#777770]";
    const latencySpan = document.createElement("span");
    latencySpan.className = "hidden px-1.5 py-0.5 rounded bg-[#e6f3ec] text-[#1f5c3a] border border-[#bfe0ce] text-[10.5px]";
    const timeSpan = document.createElement("span");
    timeSpan.textContent = nowTimeStr();

    meta.appendChild(latencySpan);
    meta.appendChild(timeSpan);
    header.appendChild(title);
    header.appendChild(meta);

    const body = document.createElement("div");
    body.className = "text-[13.5px] text-[#1a1a19] leading-relaxed whitespace-pre-wrap";

    root.appendChild(header);
    root.appendChild(body);
    return { root, body, latencySpan };
  }

  function appendCollapsibleToolCard(id, name, args) {
    hideEmptyBanner();
    const feed = $("transcript-feed");
    if (!feed) return;

    const details = document.createElement("details");
    details.id = `tool-card-${id}`;
    details.className = "rounded-md border border-[#d6cceb] bg-[#f0ecf8]/60 px-3 py-2 text-[12px]";

    const summary = document.createElement("summary");
    summary.className = "cursor-pointer font-mono font-medium text-[#3b2a63] flex items-center justify-between gap-2";
    const left = document.createElement("span");
    left.textContent = `Tool: ${name}(${JSON.stringify(args || {})})`;
    const right = document.createElement("span");
    right.id = `tool-status-${id}`;
    right.className = "text-[11px] px-1.5 py-0.5 rounded bg-white border border-[#d6cceb] text-[#453270]";
    right.textContent = "Running...";

    summary.appendChild(left);
    summary.appendChild(right);

    const pre = document.createElement("pre");
    pre.id = `tool-result-${id}`;
    pre.className = "mt-2 p-2.5 rounded bg-white border border-[#e4e4df] text-[11px] font-mono overflow-x-auto max-h-60 text-[#1a1a19]";
    pre.textContent = "Waiting for result...";

    details.appendChild(summary);
    details.appendChild(pre);
    feed.appendChild(details);
    scrollTranscriptToBottom();
  }

  function updateCollapsibleToolCard(id, name, result) {
    const statusEl = $(`tool-status-${id}`);
    const preEl = $(`tool-result-${id}`);
    if (statusEl) {
      statusEl.textContent = "Completed (click to inspect)";
      statusEl.className = "text-[11px] px-1.5 py-0.5 rounded bg-[#e6f3ec] border border-[#bfe0ce] text-[#1f5c3a]";
    }
    if (preEl) {
      preEl.textContent = JSON.stringify(result, null, 2);
    }
  }

  // =========================================================================
  // 8. Live WebSocket Session Lifecycle
  // =========================================================================
  async function connectLiveSession() {
    if (isConnected) {
      disconnectLiveSession();
      return;
    }

    await syncStateFromServer();
    ensurePlayContext();

    const btn = $("btn-connect");
    if (btn) {
      btn.textContent = "Connecting...";
      btn.disabled = true;
    }

    try {
      await startMicrophone();
    } catch (err) {
      appendSystemNotice(`Microphone permission warning: ${err.message}. Continuing in text/speaker mode.`, true);
    }

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    ws = new WebSocket(`${proto}//${location.host}/ws/live`);

    ws.onopen = () => {
      if ($("live-status-text")) $("live-status-text").textContent = "Handshaking with Gemini Live API...";
    };

    ws.onmessage = (ev) => {
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (_) {
        return;
      }

      switch (msg.type) {
        case "status":
          if ($("live-status-text")) $("live-status-text").textContent = msg.message;
          break;

        case "connected":
          isConnected = true;
          activeVoiceName = msg.voice || appConfig.voice_name || "Puck";
          if (btn) {
            btn.textContent = "Disconnect";
            btn.disabled = false;
            btn.className = "px-4 py-1.5 rounded bg-[#fae8e6] hover:bg-[#f5d5d0] text-[#8a261d] border border-[#f2cbc6] font-medium text-[12.5px] cursor-pointer transition-colors";
          }
          if ($("btn-mute")) $("btn-mute").disabled = false;
          if ($("btn-interrupt")) $("btn-interrupt").disabled = false;
          if ($("sidebar-conn-badge")) {
            $("sidebar-conn-badge").textContent = "Live";
            $("sidebar-conn-badge").className = "px-2 py-0.5 rounded bg-[#e6f3ec] border border-[#bfe0ce] text-[#1f5c3a] font-medium";
          }
          if ($("live-status-text")) {
            $("live-status-text").textContent = `Connected (${msg.model}, voice: ${msg.voice}, key: ${msg.key_name})`;
          }
          appendSystemNotice(`Connected to ${msg.model} (voice: ${msg.voice}, key: ${msg.key_name})`);
          break;

        case "input_tx":
          updateOrCreateBubble("user", msg.full || msg.text, !!msg.final);
          break;

        case "user_text_committed":
          currentUserBubble = null;
          updateOrCreateBubble("user", msg.text, true);
          break;

        case "output_tx":
          updateOrCreateBubble("gemini", msg.full || msg.text, !!msg.final);
          break;

        case "audio":
          if (msg.data) enqueuePCM24k(msg.data);
          break;

        case "interrupted":
          stopAllPlayback();
          if (currentModelBubble) {
            currentModelBubble.body.textContent += " [Interrupted]";
            currentModelBubble = null;
          }
          break;

        case "turn_complete":
          if (msg.latency_ms && msg.latency_ms > 0) {
            if (currentModelBubble && currentModelBubble.latencySpan) {
              currentModelBubble.latencySpan.textContent = `${msg.latency_ms}ms`;
              currentModelBubble.latencySpan.classList.remove("hidden");
            }
            if ($("last-latency-badge")) {
              $("last-latency-badge").textContent = `Latency: ${msg.latency_ms}ms`;
              $("last-latency-badge").classList.remove("hidden");
            }
          }
          currentUserBubble = null;
          currentModelBubble = null;
          break;

        case "tool_call":
          appendCollapsibleToolCard(msg.id, msg.name, msg.args);
          break;

        case "tool_result":
          updateCollapsibleToolCard(msg.id, msg.name, msg.result);
          break;

        case "request_hd_frame":
          if (videoStream) {
            captureAndSendFrame(true);
          }
          break;

        case "error":
          appendSystemNotice(`Error: ${msg.message}`, true);
          break;
      }
    };

    ws.onclose = () => {
      cleanupSessionUI();
    };

    ws.onerror = () => {
      appendSystemNotice("WebSocket connection error occurred.", true);
    };
  }

  function disconnectLiveSession() {
    if (ws) {
      try {
        ws.send(JSON.stringify({ type: "disconnect" }));
        ws.close();
      } catch (_) {}
      ws = null;
    }
    cleanupSessionUI();
  }

  function cleanupSessionUI() {
    const wasConnected = isConnected;
    isConnected = false;
    isMuted = false;
    currentUserBubble = null;
    currentModelBubble = null;

    stopMicrophone();
    stopAllPlayback();

    const btn = $("btn-connect");
    if (btn) {
      btn.textContent = "Start Live Session";
      btn.disabled = false;
      btn.className = "px-4 py-1.5 rounded bg-[#1a1a19] hover:bg-[#333330] text-white font-medium text-[12.5px] cursor-pointer transition-colors";
    }
    if ($("btn-mute")) {
      $("btn-mute").disabled = true;
      $("btn-mute").textContent = "Mute Mic (M)";
    }
    if ($("btn-interrupt")) $("btn-interrupt").disabled = true;
    if ($("sidebar-conn-badge")) {
      $("sidebar-conn-badge").textContent = "Offline";
      $("sidebar-conn-badge").className = "px-2 py-0.5 rounded bg-[#f4f4f0] border border-[#e4e4df] text-[#555550] font-medium";
    }
    if ($("live-status-text")) {
      $("live-status-text").textContent = `Disconnected (${appConfig.model})`;
    }
    if (wasConnected) {
      appendSystemNotice("Session disconnected and saved to Session History.");
    }
  }

  // =========================================================================
  // 9. UI Controls & Keyboard Shortcuts
  // =========================================================================
  function toggleMute() {
    if (!isConnected) return;
    isMuted = !isMuted;
    const btn = $("btn-mute");
    if (btn) {
      btn.textContent = isMuted ? "Unmute Mic (M)" : "Mute Mic (M)";
      btn.className = isMuted
        ? "px-2.5 py-1 rounded border border-[#f2cbc6] bg-[#fae8e6] text-[#8a261d] text-[11.5px] font-medium cursor-pointer"
        : "px-2.5 py-1 rounded border border-[#d4d4ce] bg-[#f9f9f7] text-[#1a1a19] text-[11.5px] cursor-pointer";
    }
    if (isMuted && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: "audio_stream_end" }));
    }
  }

  $("btn-connect")?.addEventListener("click", connectLiveSession);
  $("btn-mute")?.addEventListener("click", toggleMute);
  $("btn-interrupt")?.addEventListener("click", () => {
    stopAllPlayback();
  });

  $("btn-webcam")?.addEventListener("click", () => {
    if (visionMode === "webcam") stopVision();
    else startVision("webcam");
  });

  $("btn-screen")?.addEventListener("click", () => {
    if (visionMode === "screen") stopVision();
    else startVision("screen");
  });

  $("btn-mirror")?.addEventListener("click", () => {
    isMirrored = !isMirrored;
    applyMirrorStyle();
    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mirror_camera: isMirrored }),
    });
  });

  $("vision-res-select")?.addEventListener("change", (ev) => {
    const val = parseInt(ev.target.value, 10) || 1080;
    appConfig.vision_resolution = val;
    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ vision_resolution: val }),
    });
  });

  $("btn-snap-hd")?.addEventListener("click", () => {
    captureAndSendFrame(true);
  });

  $("btn-clear-transcript")?.addEventListener("click", () => {
    const feed = $("transcript-feed");
    if (feed) {
      feed.innerHTML = `<div id="transcript-empty" class="text-[12.5px] text-[#777770] p-4 text-center">Transcript cleared.</div>`;
    }
    currentUserBubble = null;
    currentModelBubble = null;
  });

  $("btn-copy-transcript")?.addEventListener("click", async () => {
    const feed = $("transcript-feed");
    if (!feed) return;
    const text = feed.innerText.trim();
    try {
      await navigator.clipboard.writeText(text);
      const btn = $("btn-copy-transcript");
      if (btn) {
        const prev = btn.textContent;
        btn.textContent = "Copied!";
        setTimeout(() => { btn.textContent = prev; }, 1400);
      }
    } catch (_) {}
  });

  $("text-chat-form")?.addEventListener("submit", (ev) => {
    ev.preventDefault();
    const inp = $("text-chat-input");
    if (!inp) return;
    const txt = inp.value.trim();
    if (!txt) return;
    if (!isConnected || !ws || ws.readyState !== WebSocket.OPEN) {
      appendSystemNotice("Start a Live Session first to send messages to Gemini.", true);
      return;
    }
    stopAllPlayback();
    ws.send(JSON.stringify({ type: "text", text: txt }));
    inp.value = "";
  });

  // Global Keyboard Shortcuts (Space = Interrupt, M = Mute)
  window.addEventListener("keydown", (ev) => {
    if (appConfig.voice_barge_in === false) return;
    const tag = (ev.target?.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || tag === "select" || ev.target?.isContentEditable) {
      return;
    }
    if (ev.code === "Space" && isConnected) {
      ev.preventDefault();
      stopAllPlayback();
    } else if ((ev.key === "m" || ev.key === "M") && isConnected && !ev.metaKey && !ev.ctrlKey) {
      ev.preventDefault();
      toggleMute();
    }
  });

  // Initialize on page load
  syncStateFromServer();
  populateHardwareDevices();
})();
