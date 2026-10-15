package accounting

import "testing"

func TestControlPoint115MultiCurrencyPrecision(t *testing.T) {
	t.Run("控制点 1.1.5 功能验证", func(t *testing.T) {
		usdtAmount, err := ParseDecimal("123.45678901")
		if err != nil {
			t.Fatalf("解析 USDT 金额失败: %v", err)
		}
		rate, err := ParseDecimal("7.12345678")
		if err != nil {
			t.Fatalf("解析汇率失败: %v", err)
		}

		fiatAmount, err := ConvertUSDTToFiat(usdtAmount, rate)
		if err != nil {
			t.Fatalf("USDT 转法币失败: %v", err)
		}
		if got := fiatAmount.StringFixed(CurrencyScaleFiat); got != "879.44" {
			t.Fatalf("法币金额精度错误，want=%s got=%s", "879.44", got)
		}

		roundTripUSDT, err := ConvertFiatToUSDT(fiatAmount, rate)
		if err != nil {
			t.Fatalf("法币回算 USDT 失败: %v", err)
		}
		if got := roundTripUSDT.StringFixed(CurrencyScaleUSDT); got != "123.45691525" {
			t.Fatalf("USDT 回算精度错误，want=%s got=%s", "123.45691525", got)
		}
	})
}

func TestDecimalRoundHalfEven(t *testing.T) {
	cases := []struct {
		name  string
		input string
		scale int
		want  string
	}{
		{name: "tie to even down", input: "1.00500000", scale: 2, want: "1.00"},
		{name: "tie to even up", input: "1.01500000", scale: 2, want: "1.02"},
		{name: "negative tie to even", input: "-2.22500000", scale: 2, want: "-2.22"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := ParseDecimal(tc.input)
			if err != nil {
				t.Fatalf("解析金额失败: %v", err)
			}
			rounded, err := value.Round(tc.scale)
			if err != nil {
				t.Fatalf("四舍六入五成双失败: %v", err)
			}
			if got := rounded.StringFixed(tc.scale); got != tc.want {
				t.Fatalf("结果错误，want=%s got=%s", tc.want, got)
			}
		})
	}
}

func TestDecimalRangeAndScaleValidation(t *testing.T) {
	if _, err := ParseDecimal("999999999999.99999999"); err != nil {
		t.Fatalf("Decimal(20,8) 上限值应可解析: %v", err)
	}
	if _, err := ParseDecimal("1000000000000"); err == nil {
		t.Fatalf("超出 Decimal(20,8) 上限应报错")
	}
	if _, err := ParseDecimal("1.123456789"); err == nil {
		t.Fatalf("小数位超过 8 位应报错")
	}
}

func TestDecimalArithmetic(t *testing.T) {
	left := MustParseDecimal("100.00000000")
	right := MustParseDecimal("7.12500000")

	mul, err := left.Mul(right)
	if err != nil {
		t.Fatalf("乘法失败: %v", err)
	}
	if got := mul.StringFixed(8); got != "712.50000000" {
		t.Fatalf("乘法结果错误，want=%s got=%s", "712.50000000", got)
	}

	div, err := mul.Div(right)
	if err != nil {
		t.Fatalf("除法失败: %v", err)
	}
	if got := div.StringFixed(8); got != "100.00000000" {
		t.Fatalf("除法结果错误，want=%s got=%s", "100.00000000", got)
	}

	sum, err := left.Add(right)
	if err != nil {
		t.Fatalf("加法失败: %v", err)
	}
	if got := sum.StringFixed(8); got != "107.12500000" {
		t.Fatalf("加法结果错误，want=%s got=%s", "107.12500000", got)
	}

	diff, err := left.Sub(right)
	if err != nil {
		t.Fatalf("减法失败: %v", err)
	}
	if got := diff.StringFixed(8); got != "92.87500000" {
		t.Fatalf("减法结果错误，want=%s got=%s", "92.87500000", got)
	}
}

func TestDecimalMinorUnitsAdaptation(t *testing.T) {
	t.Run("usdt atomic units keep 8-digit precision", func(t *testing.T) {
		value := MustParseDecimal("123.45678901")
		atomicUnits, err := value.MinorUnits(CurrencyScaleUSDT)
		if err != nil {
			t.Fatalf("USDT 原子单位转换失败: %v", err)
		}
		if atomicUnits != 12345678901 {
			t.Fatalf("USDT 原子单位错误，want=%d got=%d", int64(12345678901), atomicUnits)
		}

		roundTrip, err := NewDecimalFromMinorUnits(atomicUnits, CurrencyScaleUSDT)
		if err != nil {
			t.Fatalf("USDT 原子单位回算失败: %v", err)
		}
		if got := roundTrip.StringFixed(CurrencyScaleUSDT); got != "123.45678901" {
			t.Fatalf("USDT 原子单位回算精度错误，want=%s got=%s", "123.45678901", got)
		}
	})

	t.Run("fiat minor units round to 2 digits", func(t *testing.T) {
		value := MustParseDecimal("123.45678901")
		minorUnits, err := value.MinorUnits(CurrencyScaleFiat)
		if err != nil {
			t.Fatalf("法币最小单位转换失败: %v", err)
		}
		if minorUnits != 12346 {
			t.Fatalf("法币最小单位错误，want=%d got=%d", int64(12346), minorUnits)
		}

		roundTrip, err := NewDecimalFromMinorUnits(minorUnits, CurrencyScaleFiat)
		if err != nil {
			t.Fatalf("法币最小单位回算失败: %v", err)
		}
		if got := roundTrip.StringFixed(CurrencyScaleFiat); got != "123.46" {
			t.Fatalf("法币最小单位回算精度错误，want=%s got=%s", "123.46", got)
		}
	})

	t.Run("minor units overflow should fail", func(t *testing.T) {
		value := MustParseDecimal("92233720368.54775808")
		if _, err := value.MinorUnits(CurrencyScaleUSDT); err == nil {
			t.Fatalf("超出 int64 范围时应报错")
		}
	})
}
