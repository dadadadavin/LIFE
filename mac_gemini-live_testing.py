#!/usr/bin/env python3
"""mac_gemini-live_testing.py - Real-time Voice Chat with Gemini 3.8 Live (v3).

Fixes & Enhancements:
- 100% Speech Fidelity: Raw, uncorrupted 16kHz PCM audio streamed directly to Gemini (no noise gate chopping).
- Zero Echo Self-Interruption: Configured `activity_handling = NO_INTERRUPTION` and half-duplex mic suppression during speaker output.
- Multi-Turn Persistence: Continuous conversation across all turns without disconnects or timeouts.
- Language Alignment: System instruction enforces responding in the same language as the user (e.g. Indonesian/English).
- Instant Manual Barge-In: Press [Enter] anytime while Gemini is speaking to interrupt/skip.
"""

import asyncio
import os
import queue
import select
import sys
import threading
import time
from dotenv import load_dotenv
from google import genai
from google.genai import types
import pyaudio

# Audio Configuration
INPUT_RATE = 16000          # 16kHz for Gemini input
OUTPUT_RATE = 24000         # 24kHz for Gemini output
CHANNELS = 1                # Mono
AUDIO_FORMAT = pyaudio.paInt16
CHUNK_SIZE = 1024           # 1024 frames (~64ms per chunk)

# Load environment
load_dotenv()

API_KEY = (
    os.getenv("GEMINI_API_KEY")
    or os.getenv("GEMINI_API_KEY_5")
    or os.getenv("GEMINI_API_KEY_1")
)

if not API_KEY:
    print("❌ Error: No Gemini API Key found in .env file.")
    sys.exit(1)

MODEL_NAME = os.getenv("GEMINI_MODEL", "gemini-3.8-live")


def playback_worker(
    out_stream: pyaudio.Stream,
    audio_queue: queue.Queue,
    is_playing_audio: threading.Event,
    stop_event: threading.Event,
):
    """Continuously plays incoming audio chunks to Mac speakers."""
    while not stop_event.is_set():
        try:
            chunk = audio_queue.get(timeout=0.05)
            if chunk is None:
                break

            is_playing_audio.set()
            out_stream.write(chunk)
            audio_queue.task_done()
        except queue.Empty:
            # Let the hardware buffer drain slightly before declaring playback idle
            if is_playing_audio.is_set() and audio_queue.empty():
                time.sleep(0.12)
                if audio_queue.empty():
                    is_playing_audio.clear()
        except Exception as e:
            if not stop_event.is_set():
                print(f"\n[Speaker Notice]: {e}", file=sys.stderr)


async def send_microphone_stream(
    session,
    in_stream: pyaudio.Stream,
    is_playing_audio: threading.Event,
    stop_event: threading.Event,
):
    """Streams crystal-clear, unchopped microphone audio to Gemini."""
    loop = asyncio.get_running_loop()

    while not stop_event.is_set():
        try:
            # While Gemini is actively speaking through the Mac speakers,
            # pause sending mic frames so speaker sound is not fed back.
            if is_playing_audio.is_set():
                # Flush the mic hardware buffer so no speaker audio accumulates
                try:
                    avail = in_stream.get_read_available()
                    if avail > 0:
                        in_stream.read(avail, exception_on_overflow=False)
                except Exception:
                    pass
                await asyncio.sleep(0.05)
                continue

            # Gemini is listening: read clean, uncorrupted PCM audio
            pcm_chunk = await loop.run_in_executor(
                None, in_stream.read, CHUNK_SIZE, False
            )
            if not pcm_chunk or stop_event.is_set():
                continue

            # Stream clean audio directly to Gemini
            await session.send_realtime_input(
                audio=types.Blob(data=pcm_chunk, mime_type="audio/pcm;rate=16000")
            )

        except Exception as e:
            if not stop_event.is_set():
                print(f"\n[Mic Notice]: {e}", file=sys.stderr)
            break


async def receive_gemini_stream(
    session,
    audio_queue: queue.Queue,
    is_playing_audio: threading.Event,
    stop_event: threading.Event,
):
    """Persistent receiver loop that handles multi-turn audio, transcripts, and turn events."""
    while not stop_event.is_set():
        try:
            async for response in session.receive():
                if stop_event.is_set():
                    break

                server_content = response.server_content
                if server_content is None:
                    continue

                # 1. Interruption handling
                if server_content.interrupted:
                    print("\n⚡ [Interrupted]")
                    while not audio_queue.empty():
                        try:
                            audio_queue.get_nowait()
                            audio_queue.task_done()
                        except (queue.Empty, ValueError):
                            break
                    is_playing_audio.clear()

                # 2. Audio chunks
                model_turn = server_content.model_turn
                if model_turn is not None:
                    for part in model_turn.parts:
                        if part.inline_data and part.inline_data.data:
                            audio_queue.put(part.inline_data.data)
                        if part.text:
                            print(part.text, end="", flush=True)

                # 3. What User said (speech-to-text transcript)
                if server_content.input_transcription and server_content.input_transcription.text:
                    print(f"\n🗣️  You: {server_content.input_transcription.text}")
                    print(f"🤖 Gemini: ", end="", flush=True)

                # 4. What Gemini said (transcript)
                if server_content.output_transcription and server_content.output_transcription.text:
                    print(server_content.output_transcription.text, end="", flush=True)

                # 5. Turn Complete: ready for user's next speech
                if server_content.turn_complete:
                    print("\n🟢 (Gemini finished - listening to you...)")

        except asyncio.CancelledError:
            break
        except Exception as e:
            if not stop_event.is_set():
                print(f"\n[Receive notice]: {e}", file=sys.stderr)
                await asyncio.sleep(0.3)


def keyboard_listener(audio_queue: queue.Queue, is_playing_audio: threading.Event, stop_event: threading.Event):
    """Allows user to press [Enter] to instantly skip/interrupt Gemini's speech."""
    while not stop_event.is_set():
        try:
            # Check if there is user input on stdin without blocking forever
            r, _, _ = select.select([sys.stdin], [], [], 0.2)
            if r:
                line = sys.stdin.readline()
                if not line:
                    break
                # If Gemini is currently speaking, cut off playback
                if is_playing_audio.is_set() or not audio_queue.empty():
                    print("\n⚡ [Skipped by Enter key]")
                    while not audio_queue.empty():
                        try:
                            audio_queue.get_nowait()
                            audio_queue.task_done()
                        except (queue.Empty, ValueError):
                            break
                    is_playing_audio.clear()
        except Exception:
            break


async def main():
    print("=" * 70)
    print("  🎙️  Gemini 3.8 Live - Mac Voice Testing (v3 - Crystal Clear Voice)")
    print("=" * 70)
    print(f"• Model:             {MODEL_NAME}")
    print(f"• Mic:               16,000 Hz 16-bit Mono (Full audio fidelity)")
    print(f"• Speaker:           24,000 Hz 16-bit Mono")
    print(f"• Mode:              Natural Multi-turn (Anti-cut off enabled)")
    print(f"• Manual Skip:       Press [Enter] anytime to interrupt Gemini")
    print(f"• Status:            Connecting to Gemini Live API...")

    stop_event = threading.Event()
    is_playing_audio = threading.Event()
    audio_queue = queue.Queue(maxsize=200)

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

        key_thread = threading.Thread(
            target=keyboard_listener,
            args=(audio_queue, is_playing_audio, stop_event),
            daemon=True,
        )
        key_thread.start()

        client = genai.Client(api_key=API_KEY)
        config = types.LiveConnectConfig(
            response_modalities=["AUDIO"],
            input_audio_transcription=types.AudioTranscriptionConfig(),
            output_audio_transcription=types.AudioTranscriptionConfig(),
            realtime_input_config=types.RealtimeInputConfig(
                activity_handling=types.ActivityHandling.NO_INTERRUPTION,
            ),
            system_instruction=types.Content(
                parts=[
                    types.Part.from_text(
                        text=(
                            "You are a helpful, fast, and natural conversational voice assistant. "
                            "Always respond in the same language the user speaks to you (such as English or Indonesian). "
                            "Keep your spoken answers concise, clear, and direct."
                        )
                    )
                ]
            ),
        )

        async with client.aio.live.connect(model=MODEL_NAME, config=config) as session:
            print("🟢 Connected! Microphone and speaker are LIVE.")
            print("👉 Speak naturally into your Mac microphone.")
            print("👉 Press [Enter] anytime while Gemini is speaking to skip.")
            print("👉 Press [Ctrl+C] to exit.\n")
            print("-" * 70)

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
