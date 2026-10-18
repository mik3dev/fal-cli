# Audio with fal-cli

## Music

MiniMax Music 2.6 (`fal-ai/minimax-music/v2.6`, 0.15 USD per track):

`fal-cli run fal-ai/minimax-music/v2.6 -p prompt="warm lofi about a rainy night" -p lyrics="[verse] rain on the window [chorus] stay a while" --out ./music`

- `prompt` is the style description (at least 10 characters). `lyrics` is plain text with section tags (`[intro]`, `[verse]`, `[chorus]`); leave it empty for a model-written song. `is_instrumental=true` makes a track without vocals.
- Presets: `fal-cli run @musica -p prompt="..."` and `@instrumental`.
- `minimax/music-3` needs both `prompt` and `lyrics`; `duration` is a number of seconds (price per second).
- ElevenLabs music (`fal-ai/elevenlabs/music`, `elevenlabs/music/v2.5`): read the schema for `prompt` and length fields.
- The output has one audio file: `files[0].path`, `ids.audio_url`.

## Speech (ElevenLabs)

`fal-cli run fal-ai/elevenlabs/tts/multilingual-v2 -p text="Hello from fal" -p voice=Rachel --out ./voice`

- `voice` is a voice name or voice id (default `Rachel`); the schema lists the tuning fields `stability`, `similarity_boost`, `style`, `speed` (numbers) and `language_code`.
- `fal-ai/elevenlabs/tts/turbo-v2.5` is the cheaper variant with the same fields; `fal-ai/elevenlabs/tts/eleven-v3` the most expressive.
- Cost is per 1000 characters; the preset `@voz` fixes the voice, pass `text`.
- Other TTS families (`fal-ai/minimax/speech-2.8-hd`, `google/gemini-3.8-flash-tts`) have their own voice fields: read the schema.

## Sound effects and processing

- `fal-ai/elevenlabs/sound-effects/v2`: `text` describing the sound, optional duration field (see schema).
- Stems: `fal-ai/demucs` takes `audio_url` (a local file is uploaded) and returns several files (`vocals`, `drums`, `bass`, ...). `files` lists all of them in document order and `ids` has `vocals_url`, `drums_url`, ... for chaining.
- Isolate one sound: `fal-ai/sam-audio/separate` needs `audio_url` and a `prompt` describing the sound to isolate; it returns `target` (the isolated sound) and `residual` (`ids.target_url`, `ids.residual_url`).
- Voice changer, audio isolation: `fal-ai/elevenlabs/voice-changer`, `fal-ai/elevenlabs/audio-isolation` take `audio_url`.

## Chaining

A generated audio feeds a video model's `audio_url` (lip-sync, `target_audio_url` in MiniMax H3) via `ids.audio_url` or `files[0].path`.

## Recovery

- Link expired or download failed (`io`): `details.media` lists the result URLs and `details.files` what was saved; `fal-cli task wait <requestId> --out DIR` downloads again while the result lasts (minutes).
- Timeout or a lost session: `fal-cli task list` to find the `requestId`, then `fal-cli task wait <requestId>`.
- `submit_uncertain`: do not resubmit. Tell the user the request may exist and ask them to check https://fal.ai/dashboard/requests; resubmit only with their confirmation. `fal-cli task list` shows the attempt as `state: unknown` without a requestId.
- A request that failed on the model side comes back as `task_failed` with `details.errorType` (for example a content policy rejection): change the prompt, do not resubmit the same input.
