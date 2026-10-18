# Images with fal-cli

Always run `fal-cli models schema <endpoint>` first: field names differ per model. Run with `fal-cli run <endpoint> -p ... --out DIR` and read `files[].path`.

## Text to image

Prompt only:

`fal-cli run fal-ai/flux/schnell -p prompt="a fox in snow, 35mm photo" -p image_size=landscape_16_9`

- FLUX (`fal-ai/flux/schnell`, `fal-ai/flux/dev`): `image_size` is an enum (`square_hd`, `square`, `portrait_4_3`, `portrait_16_9`, `landscape_4_3`, `landscape_16_9`) or an object `{"width":1024,"height":768}` passed as JSON: `-p 'image_size={"width":1024,"height":768}'`. Optional `num_images` (1-4), `seed`, `output_format` (`jpeg`, `png`). Price per megapixel.
- Nano Banana (`fal-ai/nano-banana-pro`, `fal-ai/nano-banana-2`): `aspect_ratio` (`1:1`, `16:9`, `9:16`, `4:3`...), `resolution` (`1K`, `2K`, `4K`), `output_format` (`jpeg`, `png`, `webp`), `num_images`. Price per image.
- GPT Image (`openai/gpt-image-2`): read the schema for its size and quality fields.
- Presets: `@imagen-rapida` (FLUX schnell draft, `square_hd`), `@imagen-calidad` (Nano Banana Pro, `1:1`); pass `prompt` and override the size with `-p`.
- Several images (`num_images`): `files` lists all of them in order and `ids.images_urls` holds the URLs.

## Edit / image-to-image

The input image field name differs by model; copy it from the schema:

- `image_urls` (array): `fal-ai/nano-banana-2/edit`, `fal-ai/nano-banana-pro/edit`, `openai/gpt-image-2/edit`. Repeat the key per image.
- `image_url` (string): single-image models (the schema marks it as an image field).

Local files are uploaded for you:

`fal-cli run fal-ai/nano-banana-2/edit -p prompt="make the sky sunset orange" -p image_urls=./photo.png -p output_format=png`

Preset: `fal-cli run @imagen-edicion -p prompt="..." -p image_urls=./photo.png` (needs `prompt` and `image_urls`).

Multi-image edit or style reference: repeat `-p image_urls=...` (see the model's maximum in `models schema`).

## Chaining

A generated file can feed the next run directly: `-p image_urls=<files[0].path>` (uploaded again) or `-p image_urls=<ids.images_url>` (the fal URL, no upload). Prefer the URL while the result is fresh; prefer the local path later.

## Tips

- Prompt length limits vary by model (some require at least 3 characters, some cap at a few thousand). A long prompt can go through `-p prompt=@prompt.txt`.
- Result URLs expire minutes after completion; download (default) and report local paths.
- `--dry-run` shows the unit price before spending; the total is `unit_price` times the megapixels or images you asked for.
