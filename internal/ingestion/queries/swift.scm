; =============================================================================
; repowise — Swift symbol, import, and call queries (tree-sitter-swift)
; Note: class / struct / enum / actor all parse as class_declaration; the
; concrete kind is recovered in Go from the leading keyword token.
; =============================================================================

; ---- Imports ----
(import_declaration (identifier) @import.module) @import.statement

; ---- Symbols ----
(class_declaration name: (type_identifier) @symbol.name) @symbol.def
(protocol_declaration name: (type_identifier) @symbol.name) @symbol.def
(function_declaration name: (simple_identifier) @symbol.name) @symbol.def

; ---- Calls ----
(call_expression
  (simple_identifier) @call.target
  (call_suffix) @call.arguments) @call.site
(call_expression
  (navigation_expression
    target: (simple_identifier) @call.receiver
    suffix: (navigation_suffix suffix: (simple_identifier) @call.target))
  (call_suffix)? @call.arguments) @call.site
