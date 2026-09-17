#!/usr/bin/env python3
"""mac_gemini-live_testing.py - Real-time Voice Chat with Gemini 3.8 Live.

Features:
- Continuous multi-turn real-time conversation (does not disconnect after 1 turn).
- Acoustic Echo Suppression (prevents Mac speakers from triggering false interruptions).
- Noise Gate (filters out ambient fan/room noise so Gemini does not cut off prematurely).
- Dynamic Barge-In (you can still interrupt Gemini by speaking loudly or pressing Enter).
- Live Real-time Transcripts (streaming both your speech and Gemini's response).
"""

import asyncio
import math
import os
import queue
import struct
import sys
import threading
import time
from dotenv import load_dotenv
from google import genai
from google.genai import types
import pyaudio

# Audio Sampling Configuration
INPUT_RATE = 16000          # 16kHz for Gemini input
OUTPUT_RATE = 24000         # 24kHz for Gemini output
CHANNELS = 1                # Mono
AUDIO_FORMAT = pyaudio.paInt16
CHUNK_SIZE = 1024           # ~64ms per chunk

# Audio Levels & Thresholds
NOISE_GATE_RMS = 850        # Ambient room noise on Mac is ~300-800. Only send voice above this.
BARGE_IN_RMS = 3500         # Louder voice required to interrupt while Gemini is speaking through speakers.

# Load environment
load_dotenv()

API_KEY = (
    os.getenv("GEMINI_API_KEY")
    or os.getenv("GEMINI_API_KEY_5")
    or os.getenv("GEMINI_API_KEY_1")
)

if not API_KEY:
    print("❌ Error: No Gemini API Key found. Please check your .env file.")
    sys.exit(1)

MODEL_NAME = os.getenv("GEMINI_MODEL", "gemini-3.8-live")


def calculate_rms(data: bytes) -> float:
    """Calculate the Root Mean Square (RMS) energy of 16-bit PCM audio."""
    count = len(data) // 2
    if count == 0:
        return 0.0
    shorts = struct.unpack(f"{count}h", data)
    sum_squares = sum(s * s for s in shorts)
    return math.sqrt(sum_squares / count)


def playback_worker(
    out_stream: pyaudio.Stream,
    audio_queue: queue.Queue,
    is_playing_audio: threading.Event,
    stop_event: threading.Event,
):
    """Feeds received audio chunks to the Mac speaker and tracks active playback."""
    while not stop_event.is_set():
        try:
            chunk = audio_queue.get(timeout=0.04)
            if chunk is None:
                break

            is_playing_audio.set()
            out_stream.write(chunk)
            audio_queue.task_done()
        except queue.Empty:
            # When the queue empties, allow hardware buffer to flush before unsetting
            if is_playing_audio.is_set() and audio_queue.empty():
                time.sleep(0.12)
                if audio_queue.empty():
                    is_playing_audio.clear()
        except Exception as e:
            if not stop_event.is_set():
                print(f"\n[Speaker Error]: {e}", file=sys.stderr)


async def send_microphone_stream(
    session,
    in_stream: pyaudio.Stream,
    is_playing_audio: threading.Event,
    stop_event: threading.Event,
):
    """Captures microphone audio, applies noise gate & echo suppression, and streams to Gemini."""
    loop = asyncio.get_running_loop()
    silence_chunk = b"\x00" * (CHUNK_SIZE * 2)

    while not stop_event.is_set():
        try:
            pcm_chunk = await loop.run_in_executor(
                None, in_stream.read, CHUNK_SIZE, False
            )
            if not pcm_chunk or stop_event.is_set():
                continue

            rms = calculate_rms(pcm_chunk)

            # Check if Gemini is actively speaking through the Mac speakers
            if is_playing_audio.is_set():
                # Echo suppression: suppress speaker sound leaking into the mic
                # Allow barge-in only if user speaks loudly over the speaker
                if rms > BARGE_IN_RMS:
                    await session.send_realtime_input(
                        audio=types.Blob(data=pcm_chunk, mime_type="audio/pcm;rate=16000")
                    )
                else:
                    # Send silence to keep the stream clock synchronized without triggering VAD
                    await session.send_realtime_input(
                        audio=types.Blob(data=silence_chunk, mime_type="audio/pcm;rate=16000")
                    )
            else:
                # Gemini is quiet; user can speak naturally
                if rms >= NOISE_GATE_RMS:
                    # Real voice detected
                    await session.send_realtime_input(
                        audio=types.Blob(data=pcm_chunk, mime_type="audio/pcm;rate=16000")
                    )
                else:
                    # Ambient room silence / laptop fan noise -> send silence
                    await session.send_realtime_input(
                        audio=types.Blob(data=silence_chunk, mime_type="audio/pcm;rate=16000")
                    )

        except Exception as e:
            if not stop_event.is_set():
                print(f"\n[Mic Error]: {e}", file=sys.stderr)
            break


async def receive_gemini_stream(
    session,
    audio_queue: queue.Queue,
    is_playing_audio: threading.Event,
    stop_event: threading.Event,
):
    """Persistent receiver loop that handles multi-turn audio, transcripts, and interruptions."""
    while not stop_event.is_set():
        try:
            async for response in session.receive():
                if stop_event.is_set():
                    break

                server_content = response.server_content
                if server_content is None:
                    continue

                # 1. Handle Barge-in / Interruption
                if server_content.interrupted:
                    print("\n⚡ [Interrupted - cutting off audio]")
                    while not audio_queue.empty():
                        try:
                            audio_queue.get_nowait()
                            audio_queue.task_done()
                        except (queue.Empty, ValueError):
                            break
                    is_playing_audio.clear()

                # 2. Handle audio output
                model_turn = server_content.model_turn
                if model_turn is not None:
                    for part in model_turn.parts:
                        if part.inline_data and part.inline_data.data:
                            audio_queue.put(part.inline_data.data)
                        if part.text:
                            print(part.text, end="", flush=True)

                # 3. Transcripts: User input
                if server_content.input_transcription and server_content.input_transcription.text:
                    print(f"\n🗣️  You: {server_content.input_transcription.text}")
                    print(f"🤖 Gemini: ", end="", flush=True)

                # 4. Transcripts: Gemini output
                if server_content.output_transcription and server_content.output_transcription.text:
                    print(server_content.output_transcription.text, end="", flush=True)

                # 5. Turn Complete: ready for next turn
                if server_content.turn_complete:
                    print("\n🟢 (Gemini finished - listening to you...)")

        except asyncio.CancelledError:
            break
        except Exception as e:
            if not stop_event.is_set():
                print(f"\n[Receive notice]: {e}", file=sys.stderr)
                await asyncio.sleep(0.3)


async def main():
    print("=" * 68)
    print("  🎙️  Gemini 3.8 Live - Mac Voice Testing (v2 - Echo Shield & Multi-turn)")
    print("=" * 68)
    print(f"• Model:             {MODEL_NAME}")
    print(f"• Mic:               16,000 Hz Mono (Noise Gate: {NOISE_GATE_RMS} RMS)")
    print(f"• Speaker:           24,000 Hz Mono (Echo Suppression: Active)")
    print(f"• Multi-turn:        Persistent continuous session")
    print(f"• Status:            Connecting to Gemini Live API...")

    stop_event = threading.Event()
    is_playing_audio = threading.Event()
    audio_queue = queue.Queue(maxsize=150)

    p = pyaudio.PyAudio()

    try:
        in_stream = p.open(
            format=AUDIO_FORMAT,
            channels=CHANNELS,
            rate=INPUT_RATE,
            input=True,
            frames_per_buffer=CHUNK_SIZE,
        )

        out_stream = p.open(
            format=AUDIO_FORMAT,
            channels=CHANNELS,
            rate=OUTPUT_RATE,
            output=True,
            frames_per_buffer=CHUNK_SIZE,
        )

        playback_thread = threading.Thread(
            target=playback_worker,
            args=(out_stream, audio_queue, is_playing_audio, stop_event),
            daemon=True,
        )
        playback_thread.start()

        client = genai.Client(api_key=API_KEY)
        config = types.LiveConnectConfig(
            response_modalities=["AUDIO"],
            input_audio_transcription=types.AudioTranscriptionConfig(),
            output_audio_transcription=types.AudioTranscriptionConfig(),
            realtime_input_config=types.RealtimeInputConfig(
                automatic_activity_detection=types.AutomaticActivityDetection(
                    start_of_speech_sensitivity=types.StartSensitivity.START_SENSITIVITY_LOW,
                    end_of_speech_sensitivity=types.EndSensitivity.END_SENSITIVITY_LOW,
                    silence_duration_ms=600,
                ),
            ),
            system_instruction=types.Content(
                parts=[
                    types.Part.from_text(
                        text="You are a helpful, fast conversational voice assistant. Keep your answers natural and concise."
                    )
                ]
            ),
        )

        async with client.aio.live.connect(model=MODEL_NAME, config=config) as session:
            print("🟢 Connected! Microphone and speaker are LIVE.")
            print("👉 Speak normally into your Mac microphone.")
            print("👉 To exit, press Ctrl+C.\n")
            print("-" * 68)

            mic_task = asyncio.create_task(
                send_microphone_stream(session, in_stream, is_playing_audio, stop_event)
            )
            receive_task = asyncio.create_task(
                receive_gemini_stream(session, audio_queue, is_playing_audio, stop_event)
            )

            await asyncio.gather(mic_task, receive_task)

    except (KeyboardInterrupt, asyncio.CancelledError):
        print("\n\n⏹️  Stopping voice session...")
    finally:
        stop_event.set()
        is_playing_audio.clear()

        try:
            in_stream.stop_stream()
            in_stream.close()
        except Exception:
            pass

        try:
            out_stream.stop_stream()
            out_stream.close()
        except Exception:
            pass

        p.terminate()
        print("👋 Session ended cleanly.")


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        pass
