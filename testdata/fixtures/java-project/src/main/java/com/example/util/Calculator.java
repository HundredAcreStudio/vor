package com.example.util;

public class Calculator {
    private final int base;

    public Calculator(int base) {
        this.base = base;
    }

    public int add(int a, int b) {
        return a + b + this.base;
    }
}
