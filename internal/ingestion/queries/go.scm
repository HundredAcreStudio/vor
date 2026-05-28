; =============================================================================
; vor — Go symbol and import queries
; tree-sitter-go >= 0.23
; Ported from Python vor; capture names must remain stable across the port.
; =============================================================================

; ---------------------------------------------------------------------------
; Symbols
; ---------------------------------------------------------------------------

; Top-level function
(function_declaration
  name: (identifier) @symbol.name
  parameters: (parameter_list) @symbol.params
) @symbol.def

; Method with receiver — @symbol.receiver is used to determine parent type
(method_declaration
  receiver: (parameter_list) @symbol.receiver
  name: (field_identifier) @symbol.name
  parameters: (parameter_list) @symbol.params
) @symbol.def

; Type declaration (struct, interface, alias)
(type_spec
  name: (type_identifier) @symbol.name
) @symbol.def

; Package-level const
(const_spec
  name: (identifier) @symbol.name
) @symbol.def

; Package-level var
(var_spec
  name: (identifier) @symbol.name
) @symbol.def

; ---------------------------------------------------------------------------
; Imports
; ---------------------------------------------------------------------------

(import_spec
  (interpreted_string_literal) @import.module
) @import.statement

; ---------------------------------------------------------------------------
; Calls
; ---------------------------------------------------------------------------

(call_expression
  function: (identifier) @call.target
  arguments: (argument_list) @call.arguments
) @call.site

(call_expression
  function: (selector_expression
    operand: (identifier) @call.receiver
    field: (field_identifier) @call.target
  )
  arguments: (argument_list) @call.arguments
) @call.site

(call_expression
  function: (selector_expression
    operand: (call_expression)
    field: (field_identifier) @call.target
  )
  arguments: (argument_list) @call.arguments
) @call.site
