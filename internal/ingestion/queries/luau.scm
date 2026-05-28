; =============================================================================
; vor — Lua / Luau symbol, import, and call queries (tree-sitter-lua)
; Luau is a Lua superset; the Lua grammar parses it for symbol/import/call
; extraction. require(...) is a plain call recovered as an import.
; =============================================================================

; ---- Symbols (function name may be an identifier or a dotted function_name) ----
(function_statement name: (_) @symbol.name) @symbol.def

; ---- Imports (require "mod") ----
(function_call
  prefix: (identifier) @import.kind
  args: (function_arguments (string) @import.module)) @import.statement

; ---- Calls ----
(function_call
  prefix: (identifier) @call.target
  args: (function_arguments) @call.arguments) @call.site
