# Video with fal-cli

Video costs the most. Before running: `fal-cli models price <endpoint>` (price per second or per video), then optionally `--dry-run`. Video models wait up to 30m by default; for several clips use `--no-wait` and `fal-cli task wait <requestId>` right away (results expire minutes after completion).

Field names for the same idea differ by model. Read `fal-cli models schema <endpoint>` and use exactly what it lists. Durations are strings in Kling (`duration: "5"`) and numbers in MiniMax (`duration: 5`): the schema says which.

## Text to video

`fal-cli run fal-ai/kling-video/v2.5-turbo/pro/text-to-video -p prompt="a drone shot over a misty forest" -p duration=5 -p aspect_ratio=16:9 --out ./media`

- Kling 2.5 turbo pro: `duration` (`"5"`, `"10"`), `aspect_ratio` (`16:9`, `9:16`, `1:1`), `negative_prompt`, `cfg_scale`. 0.07 USD per second.
- MiniMax H3 (`minimax/h3-max-turbo/text-to-video`, `minimax/h3-max/text-to-video`): `prompt_expansion_mode` is required (`balanced`), `resolution` (`480P`, `768P`, `1080P`), `duration` (number), `aspect_ratio`. The turbo one is the cheapest option.
- Veo 3.1 fast (`fal-ai/veo3.1/fast`): read the schema for `duration`, `aspect_ratio`, `resolution` and the audio flag.
- Presets: `@video-calidad` (Kling 2.5 turbo, 5 s), `@video-barato` (MiniMax H3 turbo, 480P, 5 s). Pass `prompt`.

## Image to video (first frame)

- Kling 2.5 turbo pro: `image_url` (string) plus `prompt`:
  `fal-cli run fal-ai/kling-video/v2.5-turbo/pro/image-to-video -p prompt="the cat turns its head" -p image_url=./cat.png -p duration=5`
- MiniMax H3 (`minimax/h3-max-turbo/image-to-video`), Kling 3 (`fal-ai/kling-video/v3/pro/image-to-video`): read the schema for the image field name.
- Preset: `@video-imagen` needs `prompt` and `image_url`.

## First and last frame

- Kling 2.5 turbo pro image-to-video: `image_url` (first) plus `tail_image_url` (last). Preset `@video-inicio-fin` needs `prompt`, `image_url` and `tail_image_url`.
- Veo 3.1 fast: `fal-ai/veo3.1/fast/first-last-frame-to-video` (read the schema for its two image fields; 0.15 USD per second).

## Chaining

- The previous clip's URL is in `ids.video_url` and its file in `files[0].path`. Models that take a video (`video_url`, for example `fal-ai/kling-video/o3/pro/video-to-video/edit`, lip-sync models with `video_url` + `audio_url`) accept either; a local path is uploaded.
- A generated image can be the first frame: `-p image_url=<ids.images_url>` from an image run.
- `ids.duration` is set when the output reports the clip length.

## Queue control

- `fal-cli run ... --no-wait` returns `requestId`; `fal-cli task get <requestId>` shows `IN_QUEUE` (with `queuePosition`), `IN_PROGRESS` or `COMPLETED`.
- `fal-cli task cancel <requestId>` drops a queued request; `ALREADY_COMPLETED` means it was too late (and charged).
- `fal-cli task wait <requestId> --out DIR --timeout 30m` resumes a wait after a `timeout` error or in a new session.

## Tips

- Pick the cheapest model that fits for drafts (`@video-barato`), then re-run the final with the better one.
- `models schema` shows enums and defaults; a `validation` error names the problem fields.
