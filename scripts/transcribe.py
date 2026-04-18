# Copyright 2026 Marcelo Cantos
# SPDX-License-Identifier: Apache-2.0
"""Transcribe (and optionally diarise) raw PCM f32 audio from stdin.

Usage (invoked by WhisperxTranscriber via uv):
  uv run --with faster-whisper --with scipy \\
         --with pyannote.audio --with torch --with torchaudio \\
         python scripts/transcribe.py \\
         --sample-rate 48000 --channels 2 [--model large-v3] [--diarize]

Reads interleaved f32-LE samples from stdin.
Writes NDJSON segments to stdout:
  {"start_ms": N, "end_ms": N, "text": "..."}                 (without --diarize)
  {"start_ms": N, "end_ms": N, "text": "...", "speaker_id": "SPEAKER_00"}  (with)

Never writes audio to disk. HF_HUB_OFFLINE=1 is expected to be set by caller.
When --diarize is used, HF_TOKEN must be set (or the token cached via `huggingface-cli login`)
so that pyannote's gated model can be loaded from the local cache.
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
    p.add_argument(
        "--diarize",
        action="store_true",
        default=False,
        help="Run pyannote speaker diarisation and add speaker_id to each segment",
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


def transcribe(audio: np.ndarray, model_name: str, diarize: bool) -> None:
    """Run faster-whisper (and optionally pyannote) on mono 16 kHz audio and emit NDJSON.

    When diarize=False the output schema is:
      {"start_ms": N, "end_ms": N, "text": "..."}

    When diarize=True the output schema is:
      {"start_ms": N, "end_ms": N, "text": "...", "speaker_id": "SPEAKER_00"}

    All processing is done in-memory. No audio or intermediate data is written to disk.
    """
    import whisperx

    # ------------------------------------------------------------------ #
    # 1. Transcription (word-level timestamps)                            #
    # ------------------------------------------------------------------ #
    # whisperx wraps faster-whisper and adds forced-alignment for word
    # timestamps. We use the same mono 16 kHz array throughout.
    device = "cpu"
    compute_type = "int8"

    model = whisperx.load_model(model_name, device=device, compute_type=compute_type)
    result = model.transcribe(audio, batch_size=16)

    # Alignment produces word-level timestamps in result["segments"].
    model_a, metadata = whisperx.load_align_model(
        language_code=result["language"], device=device
    )
    result = whisperx.align(
        result["segments"], model_a, metadata, audio, device, return_char_alignments=False
    )

    if not diarize:
        for seg in result["segments"]:
            record = {
                "start_ms": int(seg["start"] * 1000),
                "end_ms": int(seg["end"] * 1000),
                "text": seg["text"].strip(),
            }
            sys.stdout.write(json.dumps(record) + "\n")
            sys.stdout.flush()
        return

    # ------------------------------------------------------------------ #
    # 2. Diarisation (pyannote — entirely in-memory)                      #
    # ------------------------------------------------------------------ #
    # pyannote.audio's Pipeline.from_pretrained accepts a waveform dict
    # with 'waveform' (torch.Tensor, shape [channels, samples]) and
    # 'sample_rate'. We pass the 16 kHz mono numpy array converted to a
    # 2-D torch tensor so no temp file is ever created.
    import torch
    from pyannote.audio import Pipeline

    hf_token = os.environ.get("HF_TOKEN") or True  # True = use cached token
    pipeline = Pipeline.from_pretrained(
        "pyannote/speaker-diarization-3.1",
        use_auth_token=hf_token,
    )

    # Build the in-memory audio dict pyannote expects.
    waveform_tensor = torch.from_numpy(audio[np.newaxis, :])  # shape: [1, samples]
    audio_dict = {"waveform": waveform_tensor, "sample_rate": 16_000}
    diarization = pipeline(audio_dict)

    # ------------------------------------------------------------------ #
    # 3. Merge — assign speaker IDs to whisperx segments                 #
    # ------------------------------------------------------------------ #
    result = whisperx.assign_word_speakers(diarization, result)

    for seg in result["segments"]:
        record = {
            "start_ms": int(seg["start"] * 1000),
            "end_ms": int(seg["end"] * 1000),
            "text": seg["text"].strip(),
            "speaker_id": seg.get("speaker", "SPEAKER_UNKNOWN"),
        }
        sys.stdout.write(json.dumps(record) + "\n")
        sys.stdout.flush()


def main() -> None:
    args = parse_args()
    try:
        audio = read_pcm_mono(args.sample_rate, args.channels)
        audio = resample_to_16k(audio, args.sample_rate)
        transcribe(audio, args.model, args.diarize)
    except Exception as exc:
        print(f"transcribe.py error: {exc}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
