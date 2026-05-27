export class Calculator {
    private base: number;

    constructor(base: number) {
        this.base = base;
    }

    public add(a: number, b: number): number {
        return a + b + this.base;
    }
}
