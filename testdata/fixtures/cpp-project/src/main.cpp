#include "calc.h"
#include "greeter.h"
#include <iostream>

int main() {
    int n = add(2, 3);
    std::cout << greet("world") << " " << n << std::endl;
    return 0;
}
