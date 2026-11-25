## pf find

Pixel scanning and wait utilities

### Options

```
  -h, --help[=true|false]   help for find
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

* [pf](pf.md)	 - perfuncted — screen automation CLI
* [pf find color](pf_find_color.md)	 - Find the first pixel matching a colour within tolerance
* [pf find image](pf_find_image.md)	 - Find fuzzy matches for a PNG template
* [pf find scan-for](pf_find_scan-for.md)	 - Scan multiple regions until one matches its expected hash
* [pf find text](pf_find_text.md)	 - Find rendered text using an explicitly selected local OCR provider
* [pf find wait-for](pf_find_wait-for.md)	 - Wait until a region's pixel hash equals the provided hash
* [pf find wait-for-change](pf_find_wait-for-change.md)	 - Wait until a region's pixel hash changes from an initial value
* [pf find wait-for-no-change](pf_find_wait-for-no-change.md)	 - Wait until a region's pixel hash is stable for N consecutive samples
* [pf find wait-for-visible-change](pf_find_wait-for-visible-change.md)	 - Wait until a region's visible content changes (useful for animations/loads)

