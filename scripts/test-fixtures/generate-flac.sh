#!/bin/sh
set -eu

output=${1:?usage: generate-flac.sh OUTPUT_DIRECTORY}
mkdir -p "$output"

ffmpeg -hide_banner -loglevel error -f lavfi -i 'sine=frequency=440:duration=0.25' -ac 1 -ar 44100 -sample_fmt s16 "$output/mono-16-44100.flac"
ffmpeg -hide_banner -loglevel error -f lavfi -i 'sine=frequency=880:duration=0.25' -ac 2 -ar 96000 -sample_fmt s32 "$output/stereo-24-96000.flac"
cp "$output/mono-16-44100.flac" "$output/truncated.flac"
truncate -s 128 "$output/truncated.flac"
printf 'not a flac stream\n' >"$output/fake.flac"
printf 'sidecar evidence\n' >"$output/notes.txt"
