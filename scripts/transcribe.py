# Copyright 2026 Marcelo Cantos
# SPDX-License-Identifier: Apache-2.0
"""Transcribe raw PCM f32 audio from stdin using faster-whisper.

Usage (invoked by WhisperxTranscriber via uv):
  uv run --with faster-whisper --with scipy python scripts/transcribe.py \
      --sample-rate 48000 --channels 2 [--model large-v3]

Reads interleaved f32-LE samples from stdin.
Writes NDJSON segments to stdout: {"start_ms": N, "end_ms": N, "text": "..."}.
Never writes audio to disk. HF_HUB_OFFLINE=1 is expected to be set by caller.
"""

import argparse
import json
import sys
import os

import numpy as np


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Transcribe raw PCM from stdin via faster-whisper")
    p.add_argument("--sample-rate", type=int, required=True, help="Input sample rate (Hz)")
    p.add_argument("--channels", type=int, required=True, help="Number of input channels")
    p.add_argument(
        "--model",
        default="large-v3",
        help="faster-whisper model name or path (default: large-v3)",
    )
    return p.parse_args()


def read_pcm_mono(sample_rate: int, channels: int) -> np.ndarray:
    """Read all stdin bytes, interpret as interleaved f32-LE, downmix to mono."""
    raw = sys.stdin.buffer.read()
    if len(raw) == 0:
        return np.zeros(0, dtype=np.float32)
    if len(raw) % 4 != 0:
        raise ValueError(
            f"stdin byte count {len(raw)} is not a multiple of 4 (float32 size)"
        )
    samples = np.frombuffer(raw, dtype="<f4")  # little-endian float32
    if channels > 1:
        # Reshape to (frames, channels) and average channels to mono.
        n_frames = len(samples) // channels
        samples = samples[: n_frames * channels].reshape(n_frames, channels).mean(axis=1)
    return samples.astype(np.float32)


def resample_to_16k(audio: np.ndarray, orig_rate: int) -> np.ndarray:
    """Resample audio to 16 kHz using scipy.signal.resample_poly."""
    target_rate = 16_000
    if orig_rate == target_rate:
        return audio
    from math import gcd
    g = gcd(orig_rate, target_rate)
    up = target_rate // g
    down = orig_rate // g
    from scipy.signal import resample_poly
    resampled = resample_poly(audio, up, down)
    return resampled.astype(np.float32)


def transcribe(audio: np.ndarray, model_name: str) -> None:
    """Run faster-whisper on mono 16 kHz audio and emit NDJSON to stdout."""
    from faster_whisper import WhisperModel

    model = WhisperModel(model_name, device="cpu", compute_type="int8")
    segments, _info = model.transcribe(audio, beam_size=5)
    for seg in segments:
        record = {
            "start_ms": int(seg.start * 1000),
            "end_ms": int(seg.end * 1000),
            "text": seg.text.strip(),
        }
        sys.stdout.write(json.dumps(record) + "\n")
        sys.stdout.flush()


def main() -> None:
    args = parse_args()
    try:
        audio = read_pcm_mono(args.sample_rate, args.channels)
        audio = resample_to_16k(audio, args.sample_rate)
        transcribe(audio, args.model)
    except Exception as exc:
        print(f"transcribe.py error: {exc}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
