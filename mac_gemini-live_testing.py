#!/usr/bin/env python3
"""mac_gemini-live_testing.py - Real-time Voice Chat with Gemini 3.8 Live.

Connects your Mac microphone directly to Gemini 3.8 Live and plays responses
through your Mac speakers with ultra-low latency and barge-in support.
"""

import asyncio
import os
import queue
import sys
import threading
from dotenv import load_dotenv
from google import genai
from google.genai import types
import pyaudio

# Audio Configuration
INPUT_RATE = 16000      # Gemini Live expects 16kHz audio input
OUTPUT_RATE = 24000     # Gemini Live outputs 24kHz audio
CHANNELS = 1            # Mono
AUDIO_FORMAT = pyaudio.paInt16
CHUNK_SIZE = 1024       # Frames per buffer (~64ms chunks)

# Load environment variables
load_dotenv()

# Select API Key
API_KEY = (
    os.getenv("GEMINI_API_KEY")
    or os.getenv("GEMINI_API_KEY_5")
    or os.getenv("GEMINI_API_KEY_1")
)

if not API_KEY:
    print("❌ Error: No Gemini API Key found. Please configure your .env file.")
    sys.exit(1)

MODEL_NAME = os.getenv("GEMINI_MODEL", "gemini-3.8-live")


def playback_worker(out_stream: pyaudio.Stream, audio_queue: queue.Queue, stop_event: threading.Event):
    """Worker thread that continuously feeds incoming PCM audio to the Mac speaker."""
    while not stop_event.is_set():
        try:
            chunk = audio_queue.get(timeout=0.05)
            if chunk is None:
                break
            out_stream.write(chunk)
            audio_queue.task_done()
        except queue.Empty:
            continue
        except Exception as e:
            if not stop_event.is_set():
                print(f"\n[Speaker Error]: {e}", file=sys.stderr)


async def send_microphone_stream(session, in_stream: pyaudio.Stream, stop_event: threading.Event):
    """Continuously captures audio from the microphone and streams it to Gemini."""
    loop = asyncio.get_running_loop()
    while not stop_event.is_set():
        try:
            # Read chunk without blocking the async event loop
            pcm_chunk = await loop.run_in_executor(
                None, in_stream.read, CHUNK_SIZE, False
            )
            if pcm_chunk and not stop_event.is_set():
                await session.send_realtime_input(
                    audio=types.Blob(data=pcm_chunk, mime_type="audio/pcm;rate=16000")
                )
        except Exception as e:
            if not stop_event.is_set():
                print(f"\n[Mic Streaming Error]: {e}", file=sys.stderr)
            break


async def receive_gemini_stream(session, audio_queue: queue.Queue, stop_event: threading.Event):
    """Receives streaming responses (audio, transcriptions, interruptions) from Gemini."""
    try:
        async for response in session.receive():
            if stop_event.is_set():
                break

            server_content = response.server_content
            if server_content is None:
                continue

            # 1. Barge-in / Interruption handling: flush speaker queue immediately
            if server_content.interrupted:
                print("\n⚡ [Interrupted by user - cutting off audio]")
                while not audio_queue.empty():
                    try:
                        audio_queue.get_nowait()
                        audio_queue.task_done()
                    except (queue.Empty, ValueError):
                        break

            # 2. Process audio parts from model turn
            model_turn = server_content.model_turn
            if model_turn is not None:
                for part in model_turn.parts:
                    if part.inline_data and part.inline_data.data:
                        audio_queue.put(part.inline_data.data)
                    if part.text:
                        print(part.text, end="", flush=True)

            # 3. Live Transcripts (What Gemini says)
            if server_content.output_transcription and server_content.output_transcription.text:
                print(server_content.output_transcription.text, end="", flush=True)

            # 4. Live Transcripts (What User said)
            if server_content.input_transcription and server_content.input_transcription.text:
                print(f"\n🗣️  You: {server_content.input_transcription.text}")
                print(f"🤖 Gemini: ", end="", flush=True)

            if server_content.turn_complete:
                print()  # Line break after speech turn finishes

    except asyncio.CancelledError:
        pass
    except Exception as e:
        if not stop_event.is_set():
            print(f"\n[Receive Error]: {e}", file=sys.stderr)


async def main():
    print("=" * 65)
    print("  🎙️  Gemini 3.8 Live - Mac Voice Testing")
    print("=" * 65)
    print(f"• Model:      {MODEL_NAME}")
    print(f"• Mic:        16,000 Hz, 16-bit Mono PCM")
    print(f"• Speaker:    24,000 Hz, 16-bit Mono PCM")
    print(f"• Status:     Connecting to Gemini Live API...")

    stop_event = threading.Event()
    audio_queue = queue.Queue(maxsize=100)

    # Initialize PyAudio
    p = pyaudio.PyAudio()

    try:
        # Open Microphone (input stream)
        in_stream = p.open(
            format=AUDIO_FORMAT,
            channels=CHANNELS,
            rate=INPUT_RATE,
            input=True,
            frames_per_buffer=CHUNK_SIZE,
        )

        # Open Speaker (output stream)
        out_stream = p.open(
            format=AUDIO_FORMAT,
            channels=CHANNELS,
            rate=OUTPUT_RATE,
            output=True,
            frames_per_buffer=CHUNK_SIZE,
        )

        # Start background playback thread
        playback_thread = threading.Thread(
            target=playback_worker,
            args=(out_stream, audio_queue, stop_event),
            daemon=True,
        )
        playback_thread.start()

        # Configure Live API session
        client = genai.Client(api_key=API_KEY)
        config = types.LiveConnectConfig(
            response_modalities=["AUDIO"],
            input_audio_transcription=types.AudioTranscriptionConfig(),
            output_audio_transcription=types.AudioTranscriptionConfig(),
            system_instruction=types.Content(
                parts=[
                    types.Part.from_text(
                        text="You are a helpful, direct, and fast conversational voice assistant. Respond conversationally in real-time."
                    )
                ]
            ),
        )

        async with client.aio.live.connect(model=MODEL_NAME, config=config) as session:
            print("🟢 Connected! Microphone and speaker are LIVE.")
            print("👉 Say something to Gemini (e.g. 'Hello Gemini!').")
            print("👉 Press Ctrl+C anytime to exit.\n")
            print("-" * 65)

            mic_task = asyncio.create_task(
                send_microphone_stream(session, in_stream, stop_event)
            )
            receive_task = asyncio.create_task(
                receive_gemini_stream(session, audio_queue, stop_event)
            )

            # Wait until one of the tasks finishes or user cancels
            await asyncio.gather(mic_task, receive_task)

    except KeyboardInterrupt:
        print("\n\n⏹️  Stopping voice session...")
    finally:
        stop_event.set()
        # Clean up PyAudio streams
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
