; =============================================================================
; vor — Ruby symbol, import, and call queries (tree-sitter-ruby)
; =============================================================================

; ---- Symbols ----
(module name: (constant) @symbol.name) @symbol.def
(class  name: (constant) @symbol.name) @symbol.def
(method name: (identifier) @symbol.name
  parameters: (method_parameters)? @symbol.params) @symbol.def
(singleton_method name: (identifier) @symbol.name) @symbol.def

; ---- Imports (require / require_relative are plain calls) ----
(call
  method: (identifier) @import.kind
  arguments: (argument_list (string (string_content) @import.module))) @import.statement

; ---- Calls ----
(call
  receiver: (_)? @call.receiver
  method: (identifier) @call.target
  arguments: (argument_list)? @call.arguments) @call.site
