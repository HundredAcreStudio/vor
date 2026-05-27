namespace Demo.Util
{
    public class Calculator
    {
        private readonly int _base;

        public Calculator(int baseVal)
        {
            _base = baseVal;
        }

        public int Add(int a, int b)
        {
            return a + b + _base;
        }
    }
}
