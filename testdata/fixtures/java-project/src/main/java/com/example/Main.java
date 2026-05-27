package com.example;

import com.example.util.Greeter;
import com.example.util.Calculator;
import java.util.List;

public class Main {
    public static void main(String[] args) {
        Greeter g = new Greeter();
        Calculator c = new Calculator(10);
        System.out.println(g.greet("world: " + c.add(2, 3)));
    }
}
