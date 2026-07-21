"""
Generate a test WAV file with English speech for Translator manual testing.
Uses Edge TTS (built into Windows) or falls back to a simple tone.
"""
import subprocess
import struct
import math
import os

OUTPUT = "test_speech.wav"
SAMPLE_RATE = 16000
DURATION = 3.0  # seconds

def generate_tone_wav(path):
    """Generate a 440Hz sine tone as fallback test audio."""
    num_samples = int(SAMPLE_RATE * DURATION)
    samples = []
    for i in range(num_samples):
        t = i / SAMPLE_RATE
        # A4 (440Hz) tone with fade in/out
        envelope = min(1.0, i / 1000.0, (num_samples - i) / 1000.0)
        sample = int(16000 * envelope * math.sin(2 * math.pi * 440 * t))
        samples.append(sample)

    with open(path, "wb") as f:
        # RIFF header
        data_size = num_samples * 2
        f.write(b"RIFF")
        f.write(struct.pack("<I", 36 + data_size))
        f.write(b"WAVE")
        # fmt chunk
        f.write(b"fmt ")
        f.write(struct.pack("<I", 16))          # chunk size
        f.write(struct.pack("<H", 1))           # PCM
        f.write(struct.pack("<H", 1))           # mono
        f.write(struct.pack("<I", SAMPLE_RATE))
        f.write(struct.pack("<I", SAMPLE_RATE * 2))  # byte rate
        f.write(struct.pack("<H", 2))           # block align
        f.write(struct.pack("<H", 16))          # bits per sample
        # data chunk
        f.write(b"data")
        f.write(struct.pack("<I", data_size))
        for s in samples:
            f.write(struct.pack("<h", s))

    print(f"Generated tone WAV: {path} ({DURATION}s, {SAMPLE_RATE}Hz mono)")


def generate_speech_wav(path):
    """Try Edge TTS to generate real speech, fall back to tone."""
    text = (
        "Hello, could you explain what a deadlock is "
        "and how to avoid it in Go programming language?"
    )

    # Try Edge TTS via PowerShell (built into Windows).
    ps_script = f'''
Add-Type -AssemblyName System.Speech
$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer
$synth.SelectVoice("Microsoft Zira Desktop")
$synth.Rate = 0
$synth.SetOutputToWaveFile("{path}")
$synth.Speak("{text}")
$synth.Dispose()
Write-Host "OK"
'''
    try:
        result = subprocess.run(
            ["powershell", "-NoProfile", "-Command", ps_script],
            capture_output=True, text=True, timeout=30
        )
        if result.returncode == 0 and os.path.exists(path):
            size = os.path.getsize(path)
            print(f"Generated speech WAV via Edge TTS: {path} ({size} bytes)")
            print(f"Text: {text}")
            return
        print(f"Edge TTS failed: {result.stderr[:200]}")
    except Exception as e:
        print(f"Edge TTS error: {e}")

    print("Falling back to tone WAV...")
    generate_tone_wav(path)


if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    target = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", OUTPUT)
    generate_speech_wav(target)
