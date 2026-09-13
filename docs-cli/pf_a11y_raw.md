## pf a11y raw

Invoke one typed AT-SPI primitive with an explicit handle

```
pf a11y raw [flags]
```

### Options

```
      --action-index int32        stable AT-SPI action index (default -1)
      --action-name string        exact AT-SPI action name
      --alignment string          alignment for scroll (default "anywhere")
      --bus string                AT-SPI object bus name
      --coordinate-space string   coordinate space: screen, window, or parent (default "screen")
      --end int32                 end character offset
      --generation uint           current accessibility generation
      --height int                component height
  -h, --help                      help for raw
      --index int32               child/row/column index
      --json                      write machine-readable JSON
      --offset int32              character offset
      --op string                 primitive: action, focus, scroll, scroll-to-point, set-position, set-size, set-extents, set-value, set-text-contents, replace-text, insert-text, delete-text, copy-text, cut-text, paste-text, set-caret, set-text-selection, add-text-selection, remove-text-selection, set-document-text-selections, select-child, deselect-child, select-all, clear-selection, deselect-all, deselect-selected-child, select-row, deselect-row, select-column, deselect-column, reopen
      --path string               AT-SPI object path
      --position int32            paste character position
      --range-text string         range replacement text
      --selection int32           selection number
      --selections string         JSON array of DocumentTextSelection values (default "[]")
      --start int32               start character offset
      --text string               replacement text
      --value float               new current value
      --width int                 component width
      --x int                     x coordinate
      --y int                     y coordinate
```

### Options inherited from parent commands

```
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf a11y](pf_a11y.md)	 - Inspect and operate the AT-SPI accessibility tree

