# Playback clients

Navidrome provides the OpenSubsonic endpoint and authenticates music users. Feishin and Tempus are installed on user devices; neither client is a container in the server Compose project.

## Linux with Feishin 1.15.1

Install the pinned Feishin release through its supported Linux package. Add a Navidrome/OpenSubsonic server using the Navidrome URL and a Navidrome music account. Keep stream quality at original for LAN, Wi-Fi, and fast remote links. Enable synchronized lyrics so local `.lrc` sidecars are used before the Navidrome runtime lyrics plugin.

## Android with Tempus 4.25.0

Install the pinned Tempus release, add the same Navidrome server, and sign in with a Navidrome music account. Original FLAC is the default on LAN and Wi-Fi. Offline downloads, Android Auto, gapless playback, ReplayGain, and lyrics remain client settings.

`opus-256` and `opus-160` are logical policies, not required internal profile names. A client may request the corresponding `maxBitRate`, downsampling, or server transcoding option:

- `opus-256` means roughly 256 kbps for normal cellular links.
- `opus-160` means roughly 160 kbps for constrained cellular links.

Changing transport quality does not change the FLAC master. If a client cannot express a named policy, select the closest requested bitrate manually.
