; =============================================================================
; vor — Kotlin symbol, import, and call queries (tree-sitter-kotlin)
; class / interface both parse as class_declaration; the concrete kind is
; recovered in Go from the leading keyword token.
; =============================================================================

; ---- Imports ----
(import_header (identifier) @import.module) @import.statement

; ---- Symbols ----
(class_declaration (type_identifier) @symbol.name) @symbol.def
(object_declaration (type_identifier) @symbol.name) @symbol.def
(function_declaration (simple_identifier) @symbol.name) @symbol.def

; ---- Calls ----
(call_expression
  (simple_identifier) @call.target
  (call_suffix) @call.arguments) @call.site
(call_expression
  (navigation_expression
    (simple_identifier) @call.receiver
    (navigation_suffix (simple_identifier) @call.target))
  (call_suffix)? @call.arguments) @call.site
