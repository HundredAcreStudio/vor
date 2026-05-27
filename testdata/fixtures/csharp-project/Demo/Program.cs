using System;
using Demo.Util;

namespace Demo
{
    public class Program
    {
        public static void Main(string[] args)
        {
            var calc = new Calculator(10);
            Console.WriteLine(calc.Add(2, 3));
        }
    }
}
