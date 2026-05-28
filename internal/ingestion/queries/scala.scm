; =============================================================================
; vor — Scala symbol, import, and call queries (tree-sitter-scala)
; =============================================================================

; ---- Imports (whole declaration; the leading "import " keyword is stripped
; in Go) ----
(import_declaration) @import.module @import.statement

; ---- Symbols ----
(class_definition  name: (identifier) @symbol.name) @symbol.def
(object_definition name: (identifier) @symbol.name) @symbol.def
(trait_definition  name: (identifier) @symbol.name) @symbol.def
(function_definition
  name: (identifier) @symbol.name
  parameters: (parameters)? @symbol.params) @symbol.def

; ---- Calls ----
(call_expression
  function: (identifier) @call.target
  arguments: (arguments) @call.arguments) @call.site
(call_expression
  function: (field_expression
    value: (identifier) @call.receiver
    field: (identifier) @call.target)
  arguments: (arguments) @call.arguments) @call.site
