import { Calculator } from "./calc";

export function main(): void {
    const c = new Calculator(10);
    console.log(c.add(2, 3));
}

main();
