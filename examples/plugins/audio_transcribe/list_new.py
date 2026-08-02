#!/usr/bin/env python3
"""Lists audio files under /audio that don't yet have a .done marker."""
import json
import os

AUDIO_DIR = "/audio"
AUDIO_EXTS = (".wav", ".mp3", ".m4a", ".flac", ".ogg")


def find_new_files(audio_dir):
    """Returns a sorted list of audio filenames in audio_dir that don't have
    a matching <name>.done marker file next to them."""
    files = []
    for name in sorted(os.listdir(audio_dir)):
        if not name.lower().endswith(AUDIO_EXTS):
            continue
        marker = os.path.join(audio_dir, name + ".done")
        if os.path.exists(marker):
            continue
        files.append(name)
    return files


def main():
    print(json.dumps({"files": find_new_files(AUDIO_DIR)}))


if __name__ == "__main__":
    main()
