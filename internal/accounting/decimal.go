package accounting

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

const (
	DecimalPrecision = 20
	DecimalScale     = 8

	CurrencyScaleUSDT = 8
	CurrencyScaleFiat = 2
)

var (
	decimalScaleFactor = big.NewInt(100000000)
	decimalMaxScaled   = mustDecimalBigInt("99999999999999999999")
	decimalMaxInt64    = big.NewInt(math.MaxInt64)
	decimalMinInt64    = big.NewInt(math.MinInt64)
)

type Decimal struct {
	scaled big.Int
}

func ParseDecimal(value string) (Decimal, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return Decimal{}, fmt.Errorf("decimal value is empty")
	}

	sign := 1
	if strings.HasPrefix(trimmed, "+") {
		trimmed = strings.TrimPrefix(trimmed, "+")
	} else if strings.HasPrefix(trimmed, "-") {
		sign = -1
		trimmed = strings.TrimPrefix(trimmed, "-")
	}

	if trimmed == "" {
		return Decimal{}, fmt.Errorf("invalid decimal format %q", value)
	}

	parts := strings.Split(trimmed, ".")
	if len(parts) > 2 {
		return Decimal{}, fmt.Errorf("invalid decimal format %q", value)
	}

	intPart := parts[0]
	if intPart == "" || !isDigits(intPart) {
		return Decimal{}, fmt.Errorf("invalid decimal format %q", value)
	}

	fractionPart := ""
	if len(parts) == 2 {
		fractionPart = parts[1]
		if fractionPart == "" || !isDigits(fractionPart) {
			return Decimal{}, fmt.Errorf("invalid decimal format %q", value)
		}
	}
	if len(fractionPart) > DecimalScale {
		return Decimal{}, fmt.Errorf("decimal scale exceeds %d for %q", DecimalScale, value)
	}
	if len(fractionPart) < DecimalScale {
		fractionPart += strings.Repeat("0", DecimalScale-len(fractionPart))
	}

	rawDigits := strings.TrimLeft(intPart+fractionPart, "0")
	if rawDigits == "" {
		rawDigits = "0"
	}

	scaled := new(big.Int)
	if _, ok := scaled.SetString(rawDigits, 10); !ok {
		return Decimal{}, fmt.Errorf("invalid decimal value %q", value)
	}
	if sign < 0 && scaled.Sign() != 0 {
		scaled.Neg(scaled)
	}
	return newDecimalFromScaled(scaled)
}

func MustParseDecimal(value string) Decimal {
	result, err := ParseDecimal(value)
	if err != nil {
		panic(err)
	}
	return result
}

func ZeroDecimal() Decimal {
	result, _ := newDecimalFromScaled(big.NewInt(0))
	return result
}

func (d Decimal) IsZero() bool {
	return d.scaled.Sign() == 0
}

func (d Decimal) Add(other Decimal) (Decimal, error) {
	sum := new(big.Int).Add(&d.scaled, &other.scaled)
	return newDecimalFromScaled(sum)
}

func (d Decimal) Sub(other Decimal) (Decimal, error) {
	diff := new(big.Int).Sub(&d.scaled, &other.scaled)
	return newDecimalFromScaled(diff)
}

func (d Decimal) Mul(other Decimal) (Decimal, error) {
	product := new(big.Int).Mul(&d.scaled, &other.scaled)
	rounded := roundQuotientHalfEven(product, decimalScaleFactor)
	return newDecimalFromScaled(rounded)
}

func (d Decimal) Div(other Decimal) (Decimal, error) {
	if other.IsZero() {
		return Decimal{}, fmt.Errorf("division by zero")
	}
	numerator := new(big.Int).Mul(&d.scaled, decimalScaleFactor)
	rounded := roundQuotientHalfEven(numerator, &other.scaled)
	return newDecimalFromScaled(rounded)
}

func (d Decimal) Round(scale int) (Decimal, error) {
	if scale < 0 || scale > DecimalScale {
		return Decimal{}, fmt.Errorf("round scale must be in [0,%d], got %d", DecimalScale, scale)
	}
	if scale == DecimalScale {
		return d, nil
	}

	factor := big.NewInt(pow10(DecimalScale - scale))
	quotient := roundQuotientHalfEven(&d.scaled, factor)
	rounded := new(big.Int).Mul(quotient, factor)
	return newDecimalFromScaled(rounded)
}

func NewDecimalFromMinorUnits(units int64, scale int) (Decimal, error) {
	if scale < 0 || scale > DecimalScale {
		return Decimal{}, fmt.Errorf("minor unit scale must be in [0,%d], got %d", DecimalScale, scale)
	}

	scaled := big.NewInt(units)
	if scale < DecimalScale {
		factor := big.NewInt(pow10(DecimalScale - scale))
		scaled.Mul(scaled, factor)
	}
	return newDecimalFromScaled(scaled)
}

func (d Decimal) MinorUnits(scale int) (int64, error) {
	if scale < 0 || scale > DecimalScale {
		return 0, fmt.Errorf("minor unit scale must be in [0,%d], got %d", DecimalScale, scale)
	}

	rounded := new(big.Int).Set(&d.scaled)
	if scale < DecimalScale {
		factor := big.NewInt(pow10(DecimalScale - scale))
		rounded = roundQuotientHalfEven(rounded, factor)
	}

	if rounded.Cmp(decimalMaxInt64) > 0 || rounded.Cmp(decimalMinInt64) < 0 {
		return 0, fmt.Errorf("minor units overflow int64 for Decimal(%d,%d)", DecimalPrecision, DecimalScale)
	}
	return rounded.Int64(), nil
}

func (d Decimal) StringFixed(scale int) string {
	if scale < 0 {
		scale = 0
	}
	if scale > DecimalScale {
		scale = DecimalScale
	}

	rounded := d
	if scale != DecimalScale {
		if item, err := d.Round(scale); err == nil {
			rounded = item
		}
	}

	absScaled := new(big.Int).Abs(new(big.Int).Set(&rounded.scaled))
	intPart := new(big.Int).Quo(absScaled, decimalScaleFactor)
	if scale == 0 {
		return withSignPrefix(intPart.String(), rounded.scaled.Sign() < 0)
	}
	fraction := new(big.Int).Rem(absScaled, decimalScaleFactor)
	fractionText := fmt.Sprintf("%08d", fraction.Int64())
	return withSignPrefix(intPart.String()+"."+fractionText[:scale], rounded.scaled.Sign() < 0)
}

func (d Decimal) String() string {
	text := d.StringFixed(DecimalScale)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" {
		return "0"
	}
	return text
}

func ConvertUSDTToFiat(usdtAmount, rate Decimal) (Decimal, error) {
	if rate.scaled.Sign() <= 0 {
		return Decimal{}, fmt.Errorf("rate must be greater than zero")
	}
	fiatAmount, err := usdtAmount.Mul(rate)
	if err != nil {
		return Decimal{}, err
	}
	return fiatAmount.Round(CurrencyScaleFiat)
}

func ConvertFiatToUSDT(fiatAmount, rate Decimal) (Decimal, error) {
	if rate.scaled.Sign() <= 0 {
		return Decimal{}, fmt.Errorf("rate must be greater than zero")
	}
	usdtAmount, err := fiatAmount.Div(rate)
	if err != nil {
		return Decimal{}, err
	}
	return usdtAmount.Round(CurrencyScaleUSDT)
}

func newDecimalFromScaled(scaled *big.Int) (Decimal, error) {
	if scaled == nil {
		return Decimal{}, fmt.Errorf("scaled value is nil")
	}

	absScaled := new(big.Int).Abs(new(big.Int).Set(scaled))
	if absScaled.Cmp(decimalMaxScaled) > 0 {
		return Decimal{}, fmt.Errorf("decimal out of range for Decimal(%d,%d)", DecimalPrecision, DecimalScale)
	}

	var result Decimal
	result.scaled.Set(scaled)
	return result, nil
}

func roundQuotientHalfEven(numerator, denominator *big.Int) *big.Int {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() == 0 {
		return quotient
	}

	absRemainder := new(big.Int).Abs(new(big.Int).Set(remainder))
	absDenominator := new(big.Int).Abs(new(big.Int).Set(denominator))
	doubleRemainder := new(big.Int).Lsh(absRemainder, 1)
	compare := doubleRemainder.Cmp(absDenominator)
	if compare < 0 {
		return quotient
	}

	step := int64(1)
	sign := numerator.Sign() * denominator.Sign()
	if sign < 0 {
		step = -1
	}
	if compare > 0 || quotient.Bit(0) == 1 {
		quotient.Add(quotient, big.NewInt(step))
	}
	return quotient
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func pow10(power int) int64 {
	result := int64(1)
	for i := 0; i < power; i++ {
		result *= 10
	}
	return result
}

func withSignPrefix(value string, negative bool) string {
	if negative && value != "0" {
		return "-" + value
	}
	return value
}

func mustDecimalBigInt(value string) *big.Int {
	item := new(big.Int)
	if _, ok := item.SetString(value, 10); !ok {
		panic(fmt.Sprintf("invalid decimal bigint constant %q", value))
	}
	return item
}
