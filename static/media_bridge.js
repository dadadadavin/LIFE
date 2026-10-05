// Lifel — Hardware Media & Live WebSocket Bridge (Go + HTMX Companion)
(() => {
  let appConfig = {
    model: "gemini-3.8-live",
    voice_name: "Puck",
    echo_shield: true,
    voice_barge_in: true,
    noise_gate_rms: 850,
    barge_in_threshold: 3200,
    vision_resolution: 1080,
    mirror_camera: false,
    browser_echo_cancellation: true,
    browser_noise_suppression: true,
    browser_auto_gain: false,
    push_to_talk: false,
    speaker_volume: 100,
    auto_reconnect: true,
  };

  let ws = null;
  let isConnected = false;
  let isMuted = false;
  let isPttHolding = false;
  let userInitiatedDisconnect = false;
  let isSwitchingVoice = false;
  let pingInterval = null;
  let reconnectAttempts = 0;
  const MAX_RECONNECT = 3;

  // Audio contexts & nodes
  let micCtx = null;
  let micStream = null;
  let micWorkletNode = null;
  let playCtx = null;
  let playGainNode = null;
  let nextPlayTime = 0;
  let activeSources = [];
  let lastSpeakerEndTime = 0;
  let speechTailFrames = 0;
  let silenceFlushFrames = 0;
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
            ? `On (Barge-In > ${appConfig.barge_in_threshold || 2800})`
            : "Off";
        }
        if ($("vision-res-select") && appConfig.vision_resolution) {
          $("vision-res-select").value = String(appConfig.vision_resolution);
        }
        if ($("studio-gate-slider") && appConfig.noise_gate_rms !== undefined) {
          $("studio-gate-slider").value = String(appConfig.noise_gate_rms);
          if ($("studio-gate-label")) {
            $("studio-gate-label").textContent = Number(appConfig.noise_gate_rms) === 0 ? "Off" : String(appConfig.noise_gate_rms);
          }
        }
        if ($("studio-volume-slider") && appConfig.speaker_volume !== undefined) {
          $("studio-volume-slider").value = String(appConfig.speaker_volume);
          if ($("studio-volume-label")) $("studio-volume-label").textContent = `${appConfig.speaker_volume}%`;
        }
        if (playGainNode) {
          playGainNode.gain.value = (appConfig.speaker_volume ?? 100) / 100.0;
        }
        updatePttButtonUI();
        isMirrored = !!appConfig.mirror_camera;
        applyMirrorStyle();
      }
    } catch (_) {}
  }

  function updatePttButtonUI() {
    const btn = $("btn-ptt-mode");
    if (!btn) return;
    const ptt = !!appConfig.push_to_talk;
    btn.textContent = ptt ? "PTT On (Hold V)" : "PTT Off (Open Mic)";
    btn.className = ptt
      ? "px-2 py-1 rounded border border-[#b8cee6] bg-[#e8f0f8] text-[#1f4b7a] font-medium text-[11px] cursor-pointer"
      : "px-2 py-1 rounded border border-[#d4d4ce] bg-[#f9f9f7] text-[#555550] text-[11px] cursor-pointer";
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
    if (tabName === "tools" && window.htmx) {
      window.htmx.ajax("GET", "/htmx/notes", { target: "#notes-list-container", swap: "innerHTML" });
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
  // 4. Speaker Playback (24kHz 16-bit Mono PCM + GainNode Volume) & Interruption
  // =========================================================================
  function ensurePlayContext() {
    if (!playCtx || playCtx.state === "closed") {
      playCtx = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 24000 });
      playGainNode = playCtx.createGain();
      playGainNode.gain.value = (appConfig.speaker_volume ?? 100) / 100.0;
      playGainNode.connect(playCtx.destination);
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
    src.connect(playGainNode || playCtx.destination);

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
  // 5. Microphone Capture (128ms 16kHz PCM + Echo Shield + Clean VAD Stream)
  // =========================================================================
  const WORKLET_CODE = `
    class LifelMicProcessor extends AudioWorkletProcessor {
      constructor() {
        super();
        this.targetRate = 16000;
        this.buffer = new Int16Array(2048); // 128ms at 16kHz (Standard Google Live API chunk size)
        this.offset = 0;
        this.resamplePos = 0.0;
      }
      process(inputs) {
        const input = inputs[0];
        if (!input || !input[0] || input[0].length === 0) return true;
        const chan = input[0];
        const ratio = sampleRate / this.targetRate;

        while (this.resamplePos < chan.length) {
          const idx = Math.floor(this.resamplePos);
          const s = Math.max(-1, Math.min(1, chan[idx]));
          this.buffer[this.offset++] = s < 0 ? s * 0x8000 : s * 0x7FFF;
          if (this.offset >= this.buffer.length) {
            const copy = new Int16Array(this.buffer);
            this.port.postMessage(copy.buffer, [copy.buffer]);
            this.offset = 0;
          }
          this.resamplePos += ratio;
        }
        this.resamplePos -= chan.length;
        return true;
      }
    }
    registerProcessor("lifel-mic-processor", LifelMicProcessor);
  `;

  async function startMicrophone() {
    const micDevId = $("select-mic-device")?.value;
    const audioConstraints = {
      channelCount: 1,
      echoCancellation: appConfig.browser_echo_cancellation !== false,
      noiseSuppression: appConfig.browser_noise_suppression !== false,
      autoGainControl: appConfig.browser_auto_gain !== false,
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

    const silentFrame = new Int16Array(2048).buffer;

    micWorkletNode.port.onmessage = (ev) => {
      if (!isConnected || !ws || ws.readyState !== WebSocket.OPEN) return;
      if (isMuted) {
        sendPcmBuffer(silentFrame, true);
        updateMicMeter(0, "Muted");
        return;
      }

      const pcm16 = new Int16Array(ev.data);
      let sumSq = 0;
      for (let i = 0; i < pcm16.length; i++) {
        sumSq += pcm16[i] * pcm16[i];
      }
      const rms = Math.round(Math.sqrt(sumSq / pcm16.length));

      // Push-to-Talk Mode (Hold V)
      if (appConfig.push_to_talk) {
        if (isPttHolding) {
          stopAllPlayback();
          sendPcmBuffer(ev.data, false);
          updateMicMeter(rms, "PTT Transmitting (Holding V)");
        } else {
          sendPcmBuffer(silentFrame, true);
          updateMicMeter(rms, "PTT Standby (Hold V to talk)");
        }
        return;
      }

      const spkActive = isSpeakerPlaying();
      const inCooldown = performance.now() - lastSpeakerEndTime < 220;
      const gateRms = Number(appConfig.noise_gate_rms ?? 0);
      const bargeRms = Number(appConfig.barge_in_threshold ?? 2800);

      // Smart Echo Shield + Voice Barge-In (Active ONLY while speaker is playing or cooling down)
      if (appConfig.echo_shield && (spkActive || inCooldown)) {
        if (appConfig.voice_barge_in !== false && rms >= bargeRms) {
          bargeInHotFrames++;
          if (bargeInHotFrames >= 2) {
            stopAllPlayback();
            bargeInHotFrames = 0;
            sendPcmBuffer(ev.data, false);
            updateMicMeter(rms, "Barge-In Triggered");
            return;
          }
        } else {
          bargeInHotFrames = 0;
        }
        // While speaker is outputting, send silent frames to prevent acoustic feedback loop
        sendPcmBuffer(silentFrame, true);
        updateMicMeter(rms, "Echo Shield Active");
        return;
      }

      bargeInHotFrames = 0;

      // Normal Speech Mode: Speaker is silent.
      if (gateRms <= 0) {
        // Raw mic stream directly to Gemini Live Server Neural VAD (Official Google standard)
        sendPcmBuffer(ev.data, false);
        updateMicMeter(rms, rms > 220 ? "Speaking" : "Listening");
      } else {
        // Optional noise gate if user explicitly set slider > 0
        if (rms >= gateRms) {
          speechTailFrames = 6; // ~760ms tail so word endings and pauses are never clipped
          sendPcmBuffer(ev.data, false);
          updateMicMeter(rms, "Streaming Speech");
        } else if (speechTailFrames > 0) {
          speechTailFrames--;
          sendPcmBuffer(ev.data, false);
          updateMicMeter(rms, "Speech Tail");
        } else {
          sendPcmBuffer(silentFrame, true);
          updateMicMeter(rms, `Gate Closed (< ${gateRms})`);
        }
      }
    };

    source.connect(micWorkletNode);
    populateHardwareDevices();
  }

  function sendPcmBuffer(arrayBuf, isSilent = false) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const bytes = new Uint8Array(arrayBuf);
    let binary = "";
    for (let i = 0; i < bytes.byteLength; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    ws.send(JSON.stringify({ type: "audio", data: btoa(binary), silent: isSilent }));
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
  // 6. Vision Input (Webcam / Screen / HD Snapshot / Image & File Upload)
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

  // Update #11: Upload or Drag-and-Drop Image or Text File into Live Session
  async function handleUploadedFile(file) {
    if (!file) return;
    if (!isConnected || !ws || ws.readyState !== WebSocket.OPEN) {
      appendSystemNotice("Start a Live Session first to upload an image or file to Gemini.", true);
      return;
    }

    if (file.type.startsWith("image/")) {
      const reader = new FileReader();
      reader.onload = () => {
        const img = new Image();
        img.onload = () => {
          const canvas = $("vision-canvas") || document.createElement("canvas");
          const maxDim = 1280;
          let w = img.width;
          let h = img.height;
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
          ctx.drawImage(img, 0, 0, w, h);
          const dataUrl = canvas.toDataURL("image/jpeg", 0.92);
          const b64 = dataUrl.split(",")[1];
          if (b64) {
            ws.send(JSON.stringify({ type: "image", data: b64, mime_type: "image/jpeg" }));
            ws.send(
              JSON.stringify({
                type: "text",
                text: `[Uploaded image: ${file.name} (${w}x${h})]. Please inspect this image and let me know what you see.`,
              })
            );
            if ($("vision-status-label")) {
              $("vision-status-label").textContent = `Uploaded ${file.name} (${w}x${h})`;
            }
          }
        };
        img.src = reader.result;
      };
      reader.readAsDataURL(file);
    } else {
      const text = await file.text();
      const clipped = text.length > 12000 ? text.slice(0, 12000) + "\n...[truncated]" : text;
      ws.send(
        JSON.stringify({
          type: "text",
          text: `[Uploaded file: ${file.name}]\n\`\`\`\n${clipped}\n\`\`\`\nPlease review this file.`,
        })
      );
      if ($("vision-status-label")) {
        $("vision-status-label").textContent = `Uploaded text file: ${file.name}`;
      }
    }
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
  // 8. Live WebSocket Session Lifecycle + Ping/Pong RTT + Auto-Reconnect
  // =========================================================================
  async function connectLiveSession(urlOverride = null) {
    const validUrl = typeof urlOverride === "string" && (urlOverride.startsWith("ws://") || urlOverride.startsWith("wss://")) ? urlOverride : null;
    if (isConnected && !validUrl) {
      userInitiatedDisconnect = true;
      disconnectLiveSession();
      return;
    }

    userInitiatedDisconnect = false;
    await syncStateFromServer();
    ensurePlayContext();

    const btn = $("btn-connect");
    if (btn) {
      btn.textContent = "Connecting...";
      btn.disabled = true;
    }

    if (!micStream) {
      try {
        await startMicrophone();
      } catch (err) {
        appendSystemNotice(`Microphone permission warning: ${err.message}. Continuing in text/speaker mode.`, true);
      }
    }

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = validUrl || `${proto}//${location.host}/ws/live`;
    ws = new WebSocket(url);

    ws.onopen = () => {
      if ($("live-status-text")) $("live-status-text").textContent = "Handshaking with Gemini Live API...";
      if (pingInterval) clearInterval(pingInterval);
      pingInterval = setInterval(() => {
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: "ping", ts: Date.now() }));
        }
      }, 5000);
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
          reconnectAttempts = 0;
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
          // Trigger initial RTT ping
          if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: "ping", ts: Date.now() }));
          }
          break;

        case "pong":
          if (msg.ts && $("sidebar-rtt-label")) {
            const rtt = Math.max(1, Date.now() - Number(msg.ts));
            $("sidebar-rtt-label").textContent = `${rtt}ms`;
          }
          break;

        case "usage":
          if (msg.total_tokens && $("live-tokens-badge")) {
            $("live-tokens-badge").textContent = `Tokens: ${msg.total_tokens}`;
            $("live-tokens-badge").classList.remove("hidden");
          }
          break;

        case "memory_updated":
          if (window.htmx && $("memory-list-container")) {
            window.htmx.ajax("GET", "/htmx/memory", { target: "#memory-list-container", swap: "innerHTML" });
          }
          break;

        case "notes_updated":
          if (window.htmx && $("notes-list-container")) {
            window.htmx.ajax("GET", "/htmx/notes", { target: "#notes-list-container", swap: "innerHTML" });
          }
          break;

        case "input_tx":
          updateOrCreateBubble("user", msg.full || msg.text, false);
          break;

        case "user_turn_finalized":
        case "user_text_committed":
          if (currentUserBubble) {
            currentUserBubble.body.textContent = msg.text;
            currentUserBubble = null;
          } else {
            updateOrCreateBubble("user", msg.text, true);
          }
          break;

        case "output_tx":
          if (currentUserBubble) {
            currentUserBubble = null;
          }
          updateOrCreateBubble("gemini", msg.full || msg.text, false);
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
          currentUserBubble = null;
          break;

        case "turn_complete":
          if (msg.latency_ms && msg.latency_ms > 0) {
            if (currentModelBubble && currentModelBubble.latencySpan) {
              currentModelBubble.latencySpan.textContent = `${msg.latency_ms}ms`;
              currentModelBubble.latencySpan.classList.remove("hidden");
            }
            if ($("last-latency-badge")) {
              $("last-latency-badge").textContent = `Turn: ${msg.latency_ms}ms`;
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
      const unexpectedDrop = isConnected && !userInitiatedDisconnect;
      cleanupSessionUI();
      if (unexpectedDrop && appConfig.auto_reconnect !== false) {
        if (reconnectAttempts < MAX_RECONNECT) {
          reconnectAttempts++;
          appendSystemNotice(`Connection dropped. Auto-reconnecting (${reconnectAttempts}/${MAX_RECONNECT}) in 2s with session resumption...`);
          setTimeout(() => {
            if (!isConnected && !userInitiatedDisconnect) {
              const proto = location.protocol === "https:" ? "wss:" : "ws:";
              const resumeUrl = `${proto}//${location.host}/ws/live?resume=true`;
              connectLiveSession(resumeUrl);
            }
          }, 2000);
        } else {
          reconnectAttempts = 0;
          appendSystemNotice("Unable to reconnect. Please check terminal logs and click 'Start Live Session' to retry.", true);
        }
      } else {
        reconnectAttempts = 0;
      }
    };

    ws.onerror = () => {
      if (!isConnected) {
        appendSystemNotice(`WebSocket failed to connect to ${url}. Make sure './lifel' is running in your terminal.`, true);
      } else {
        appendSystemNotice("WebSocket connection error occurred.", true);
      }
    };
  }

  async function reconnectWithVoice(newVoice) {
    if (!isConnected || !ws) return;
    activeVoiceName = newVoice;
    if ($("sidebar-voice-label")) $("sidebar-voice-label").textContent = newVoice;
    
    isSwitchingVoice = true;
    userInitiatedDisconnect = true;
    try {
      ws.send(JSON.stringify({ type: "disconnect" }));
      ws.close();
    } catch (_) {}
    ws = null;

    await new Promise((r) => setTimeout(r, 150));
    userInitiatedDisconnect = false;

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${location.host}/ws/live?voice=${encodeURIComponent(newVoice)}&resume=true`;
    await connectLiveSession(url);
    isSwitchingVoice = false;
  }

  function disconnectLiveSession() {
    userInitiatedDisconnect = true;
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
    isPttHolding = false;
    currentUserBubble = null;
    currentModelBubble = null;

    if (pingInterval) {
      clearInterval(pingInterval);
      pingInterval = null;
    }
    if ($("sidebar-rtt-label")) $("sidebar-rtt-label").textContent = "--";

    if (!isSwitchingVoice) {
      stopMicrophone();
    }
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
      $("btn-mute").className = "px-2.5 py-1 rounded border border-[#d4d4ce] bg-[#f9f9f7] text-[#1a1a19] text-[11.5px] disabled:opacity-40 cursor-pointer";
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
  // 9. UI Controls, Drag-and-Drop, Quick Chips & Keyboard Shortcuts
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
  }

  window.connectLiveSession = () => {
    connectLiveSession();
  };
  $("btn-connect")?.addEventListener("click", () => {
    connectLiveSession();
  });
  $("btn-mute")?.addEventListener("click", toggleMute);
  $("btn-interrupt")?.addEventListener("click", () => {
    stopAllPlayback();
  });

  // Push-to-Talk Mode Toggle (Update #13)
  $("btn-ptt-mode")?.addEventListener("click", () => {
    appConfig.push_to_talk = !appConfig.push_to_talk;
    updatePttButtonUI();
    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ push_to_talk: appConfig.push_to_talk }),
    });
  });

  // Mic Noise Gate RMS Slider (0 = Off / Raw Audio)
  $("studio-gate-slider")?.addEventListener("input", (ev) => {
    const gate = isNaN(parseInt(ev.target.value, 10)) ? 0 : parseInt(ev.target.value, 10);
    appConfig.noise_gate_rms = gate;
    if ($("studio-gate-label")) $("studio-gate-label").textContent = gate === 0 ? "Off" : String(gate);
  });
  $("studio-gate-slider")?.addEventListener("change", (ev) => {
    const gate = isNaN(parseInt(ev.target.value, 10)) ? 0 : parseInt(ev.target.value, 10);
    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ noise_gate_rms: gate }),
    });
  });

  // Speaker Volume GainNode Slider (Update #13)
  $("studio-volume-slider")?.addEventListener("input", (ev) => {
    const vol = parseInt(ev.target.value, 10) || 0;
    appConfig.speaker_volume = vol;
    if ($("studio-volume-label")) $("studio-volume-label").textContent = `${vol}%`;
    if (playGainNode) {
      playGainNode.gain.value = vol / 100.0;
    }
  });
  $("studio-volume-slider")?.addEventListener("change", (ev) => {
    const vol = parseInt(ev.target.value, 10) || 0;
    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ speaker_volume: vol }),
    });
  });

  // File / Image Upload & Drag-and-Drop (Update #11)
  $("btn-upload-file")?.addEventListener("click", () => {
    $("studio-file-upload")?.click();
  });
  $("studio-file-upload")?.addEventListener("change", (ev) => {
    const file = ev.target.files?.[0];
    if (file) {
      handleUploadedFile(file);
      ev.target.value = "";
    }
  });

  const dropzone = $("vision-dropzone");
  if (dropzone) {
    dropzone.addEventListener("dragover", (ev) => {
      ev.preventDefault();
      dropzone.classList.add("border-[#1f4b7a]");
    });
    dropzone.addEventListener("dragleave", () => {
      dropzone.classList.remove("border-[#1f4b7a]");
    });
    dropzone.addEventListener("drop", (ev) => {
      ev.preventDefault();
      dropzone.classList.remove("border-[#1f4b7a]");
      const file = ev.dataTransfer?.files?.[0];
      if (file) handleUploadedFile(file);
    });
  }

  // Quick Prompt Starter Chips (Update #14)
  document.querySelectorAll(".quick-prompt-chip").forEach((chip) => {
    chip.addEventListener("click", () => {
      const prompt = chip.getAttribute("data-prompt");
      if (!prompt) return;
      if (!isConnected || !ws || ws.readyState !== WebSocket.OPEN) {
        const inp = $("text-chat-input");
        if (inp) {
          inp.value = prompt;
          inp.focus();
        }
        appendSystemNotice("Prompt placed in chat box. Click Start Live Session to send.", false);
        return;
      }
      stopAllPlayback();
      ws.send(JSON.stringify({ type: "text", text: prompt }));
    });
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

  // Global Keyboard Shortcuts (Space = Interrupt, M = Mute, Hold V = Push-to-Talk)
  window.addEventListener("keydown", (ev) => {
    const tag = (ev.target?.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || tag === "select" || ev.target?.isContentEditable) {
      return;
    }
    if (ev.code === "KeyV" && isConnected && appConfig.push_to_talk && !ev.metaKey && !ev.ctrlKey) {
      isPttHolding = true;
      return;
    }
    if (appConfig.voice_barge_in === false) return;
    if (ev.code === "Space" && isConnected) {
      ev.preventDefault();
      stopAllPlayback();
    } else if ((ev.key === "m" || ev.key === "M") && isConnected && !ev.metaKey && !ev.ctrlKey) {
      ev.preventDefault();
      toggleMute();
    }
  });

  window.addEventListener("keyup", (ev) => {
    const tag = (ev.target?.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || tag === "select" || ev.target?.isContentEditable) {
      return;
    }
    if (ev.code === "KeyV" && isPttHolding) {
      isPttHolding = false;
    }
  });

  // Initialize Tool Tester default sample args on load (Update #10b)
  const toolSelect = $("tool-tester-select");
  const toolArgsInput = $("tool-tester-args");
  if (toolSelect && toolArgsInput && toolSelect.options.length > 0) {
    toolArgsInput.value = toolSelect.options[0].getAttribute("data-sample") || "{}";
  }

  // Listen for HTMX configUpdated event to trigger mid-session voice switching seamlessly
  document.body.addEventListener("configUpdated", async () => {
    const prevVoice = activeVoiceName;
    await syncStateFromServer();
    if (isConnected && appConfig.voice_name && appConfig.voice_name !== prevVoice) {
      appendSystemNotice(`Switching voice to ${appConfig.voice_name} (resuming session)...`);
      await reconnectWithVoice(appConfig.voice_name);
    }
  });

  // Initialize on page load
  syncStateFromServer();
  populateHardwareDevices();
})();
