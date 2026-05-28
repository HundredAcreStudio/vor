; =============================================================================
; vor — PHP symbol, import, and call queries (tree-sitter-php)
; =============================================================================

; ---- Imports ----
(namespace_use_declaration (namespace_use_clause) @import.module) @import.statement

; ---- Symbols ----
(class_declaration     name: (name) @symbol.name) @symbol.def
(interface_declaration name: (name) @symbol.name) @symbol.def
(trait_declaration     name: (name) @symbol.name) @symbol.def
(enum_declaration      name: (name) @symbol.name) @symbol.def
(function_definition
  name: (name) @symbol.name
  parameters: (formal_parameters) @symbol.params) @symbol.def
(method_declaration
  (visibility_modifier)? @symbol.modifiers
  name: (name) @symbol.name
  parameters: (formal_parameters) @symbol.params) @symbol.def

; ---- Calls ----
(function_call_expression
  function: (name) @call.target
  arguments: (arguments) @call.arguments) @call.site
(member_call_expression
  object: (_) @call.receiver
  name: (name) @call.target
  arguments: (arguments) @call.arguments) @call.site
(scoped_call_expression
  scope: (_) @call.receiver
  name: (name) @call.target
  arguments: (arguments) @call.arguments) @call.site
