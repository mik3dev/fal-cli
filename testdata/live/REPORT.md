# Captura real fal.ai (2026-10-18)

Schemas: 47 (errores 0) · Precios: 47 · Billing: fal.ai error 403: This API key is not permitted to perform this action.

## Endpoints por categoría (muestra)

- audio-to-audio: 43
- image-to-3d: 5
- image-to-image: 105
- image-to-video: 90
- json: 1
- llm: 2
- speech-to-text: 3
- text-to-audio: 64
- text-to-image: 87
- text-to-speech: 47
- text-to-video: 59
- video-to-audio: 1
- video-to-text: 1
- video-to-video: 72
- vision: 1

## Sin metadata

(ninguno)

## Schemas sin *Input

(ninguno)

## Errores de schema

(ninguno)

## Claves de input *_url/*_urls o con ui

- audio_url: 9
- end_image_url: 4
- file_url: 2
- first_frame_url: 1
- frontal_image_url: 2
- image_url: 4
- image_urls: 6
- last_frame_url: 1
- mask_url: 2
- middle_image_url: 1
- pdf_url: 2
- reference_audio_urls: 3
- reference_image_urls: 5
- reference_video_urls: 3
- start_image_url: 1
- tail_image_url: 1
- target_audio_url: 4
- video_url: 8
- web_url: 2

## Claves de output con $ref

- audio -> #/components/schemas/File: 14
- images -> #/components/schemas/Image: 2
- images -> #/components/schemas/ImageFile: 9
- residual -> #/components/schemas/File: 1
- target -> #/components/schemas/File: 1
- video -> #/components/schemas/File: 17
- video -> #/components/schemas/VideoFile: 2

## Unidades de precio

- 1000 characters: 7
- 5 seconds: 1
- audios: 1
- images: 4
- megapixels: 3
- minutes: 6
- seconds: 19
- units: 6

## Formas de File/Image/Video/Audio

- MusicAudioReference: audio_url,end_ms,start_ms,strength
- Image: content_type,height,url,width
- AudioSetting25: bitrate,format,sample_rate
- AudioSetting: bitrate,channel,format,sample_rate
- File: content_type,file_name,file_size,url

## Errores de pricing

(ninguno)
