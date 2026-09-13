package capability

import "slices"

var operationsBySurface = map[string][]string{
	"screen": {
		"capture",
		"hash",
		"pixel",
		"resolution",
		"wait",
		"wait-change",
		"wait-stable",
	},
	"input": {
		"keyboard",
		"pointer",
		"click",
		"scroll",
		"drag",
		"pointer-location",
		"pointer-coordinate-space",
		"sync",
	},
	"windows": {
		"discover",
		"active-title",
		"sync",
		"info",
		"activate",
		"move",
		"resize",
		"close",
		"minimize",
		"maximize",
		"fullscreen",
		"restore",
	},
	"outputs": {
		"list",
	},
	"clipboard": {
		"get",
		"set",
	},
	"accessibility": {
		"applications",
		"snapshot",
		"find",
		"find-application",
		"focused",
		"at-point",
		"events",
		"outline",
		"invoke-action",
		"invoke-action-by-name",
		"invoke-default-action",
		"grab-focus",
		"scroll",
		"scroll-to-point",
		"set-position",
		"set-size",
		"set-extents",
		"set-value",
		"set-text-contents",
		"replace-text",
		"insert-text",
		"delete-text",
		"copy-text",
		"cut-text",
		"paste-text",
		"set-caret",
		"set-text-selection",
		"add-text-selection",
		"remove-text-selection",
		"set-document-text-selections",
		"select-child",
		"deselect-child",
		"select-all",
		"clear-selection",
		"deselect-all",
		"deselect-selected-child",
		"select-row",
		"deselect-row",
		"select-column",
		"deselect-column",
		"window-root",
		"reopen",
	},
}

// Operations returns a defensive copy of the canonical operation names for a
// capability surface, optionally omitting operations unsupported by a backend.
func Operations(surface string, exclude ...string) []string {
	operations := slices.Clone(operationsBySurface[surface])
	for _, name := range exclude {
		operations = slices.DeleteFunc(operations, func(operation string) bool {
			return operation == name
		})
	}
	return operations
}
