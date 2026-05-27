use rustdemo::calc::add;
use rustdemo::greeter::greet;
use std::collections::HashMap;

fn main() {
    let n = add(2, 3);
    println!("{}", greet(&format!("world ({})", n)));
    let _m: HashMap<String, i32> = HashMap::new();
}
