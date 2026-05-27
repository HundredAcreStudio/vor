// This module is intentionally not declared in lib.rs and not imported
// by any other file — it's the dead-code anchor for the integration
// test.

pub fn never_called() -> i32 {
    42
}
