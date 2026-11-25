## pf find text

Find rendered text using an explicitly selected local OCR provider

### Synopsis

Capture one authorized screen region and recognize its visible text with an
explicitly selected local provider. Tesseract is optional and is never enabled
implicitly. The provider runs locally without a shell, network fallback, or
temporary screenshot files. Confidence is provider-specific, not a probability.
No matching text returns an empty successful result. Use --output json for
machine-readable matches with desktop and capture-pixel rectangles. Tesseract
verifies the selected language pack before screen capture.

Matches are contiguous OCR words on one line and are ordered top-to-bottom,
then left-to-right. Exact matching is case-sensitive and requires one ASCII
space between words. normalize-case lowercases Unicode text while preserving
space separators; normalize-whitespace collapses Unicode whitespace without
changing case; normalize applies both. JSON reports screen coordinates and
image-pixel coordinates, the source rectangle and whether --max-results
truncated the result. Tesseract confidence is on its 0–100 provider scale.

```
pf find text [flags]
```

### Examples

```
pf find text --query 'Continue' --rect 0,0,1280,720 --provider tesseract
pf find text --query 'Invoice paid' --rect 0,0,1280,720 --provider tesseract --match normalize --output json
```

### Options

```
  -h, --help[=true|false]   help for text
      --language string     Tesseract language code or combination (default "eng")
      --match string        exact|normalize-case|normalize-whitespace|normalize (default "exact")
      --max-results int     maximum matches to return (1..1024) (default 256)
      --output string       plain|json (default "plain")
      --provider string     explicit local OCR provider (tesseract)
      --query string        text phrase to find
      --rect string         capture region as x0,y0,x1,y1
      --timeout string      capture and OCR timeout (default "5s")
```

### Options inherited from parent commands

```
      --input-backend string                     input backend: auto or remote-desktop (authorized Wayland portal/EIS) (default "auto")
      --nested[=true|false]                      start and target a new nested Wayland session
      --remember-input-permission[=true|false]   request persistent authorization for remote-desktop input
      --sync[=true|false]                        sync after observable mutating commands when supported
      --trace-actions[=true|false]               print each API action to stderr as it runs
      --trace-delay duration                     sleep after each traced action
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

