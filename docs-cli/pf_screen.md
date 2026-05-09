## pf screen

Screen capture operations

### Options

```
  -h, --help   help for screen
```

### Options inherited from parent commands

```
      --max-x int32            input coordinate space width (default 1920)
      --max-y int32            input coordinate space height (default 1080)
      --nested                 auto-detect and connect to a nested Wayland session in /tmp
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf](pf.md)	 - perfuncted — screen automation CLI
* [pf screen get-all-pixels](pf_screen_get-all-pixels.md)	 - Capture the entire screen and output raw RGBA pixels to stdout
* [pf screen grab](pf_screen_grab.md)	 - Capture a screen region and save as PNG
* [pf screen grab-full-hash](pf_screen_grab-full-hash.md)	 - Auto-generated wrapper for perfuncted.GrabFullHash
* [pf screen grab-region](pf_screen_grab-region.md)	 - Capture a specific screen region
* [pf screen grab-region-hash](pf_screen_grab-region-hash.md)	 - Auto-generated wrapper for perfuncted.GrabRegionHash
* [pf screen hash](pf_screen_hash.md)	 - Print the CRC32 pixel hash of a screen region
* [pf screen pixel](pf_screen_pixel.md)	 - Print the RGB colour of a single pixel
* [pf screen resolution](pf_screen_resolution.md)	 - Print the screen resolution
* [pf screen watch](pf_screen_watch.md)	 - Continuously print hash changes in a screen region

