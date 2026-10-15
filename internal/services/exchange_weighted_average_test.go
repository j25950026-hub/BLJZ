package services

import (
	"math"
	"testing"
)

func TestControlPoint342WeightedAverage(t *testing.T) {
	t.Run("控制点 3.4.2 功能验证", func(t *testing.T) {
		prices := []float64{7.12, 7.60, 6.80, 7.10, 7.70, 7.11, 7.13, 7.14, 7.15, 7.16, 7.90}
		fairPrice, top10 := avgTop10(prices)

		expectedTop10 := []float64{6.80, 7.10, 7.11, 7.12, 7.13, 7.14, 7.15, 7.16, 7.60, 7.70}
		if len(top10) != len(expectedTop10) {
			t.Fatalf("Top10 长度错误: want=%d got=%d", len(expectedTop10), len(top10))
		}
		for i, want := range expectedTop10 {
			if !almostEqualFloat(top10[i], want, 1e-12) {
				t.Fatalf("Top10[%d] 错误: want=%.12f got=%.12f", i, want, top10[i])
			}
		}

		const expectedFairPrice = 7.1653427419354845
		if !almostEqualFloat(fairPrice, expectedFairPrice, 1e-12) {
			t.Fatalf("公允价计算错误: want=%.12f got=%.12f", expectedFairPrice, fairPrice)
		}

		fullTop10Average := averageFloatSlice(expectedTop10)
		if !(fairPrice < fullTop10Average) {
			t.Fatalf("去极值加权结果应低于包含极值的 Top10 均价: fair=%.12f top10_avg=%.12f", fairPrice, fullTop10Average)
		}

		marketFairPrice := ComputeFairPrice([]float64{7.18, 7.22, 7.65, 7.20, 6.90})
		if !almostEqualFloat(marketFairPrice, 7.20, 1e-12) {
			t.Fatalf("市场公允价应排除最高/最低报价后落在中位区间: got=%.12f", marketFairPrice)
		}
	})

	t.Run("样本不足三条时退化为普通均值", func(t *testing.T) {
		fairPrice, top10 := avgTop10([]float64{7.40, 7.20})
		if len(top10) != 2 {
			t.Fatalf("样本不足三条时应保留全部报价: got=%d", len(top10))
		}
		if !almostEqualFloat(fairPrice, 7.30, 1e-12) {
			t.Fatalf("样本不足三条时均值错误: want=7.30 got=%.12f", fairPrice)
		}
	})
}

func TestControlPoint343OutlierFiltering(t *testing.T) {
	t.Run("控制点 3.4.3 功能验证", func(t *testing.T) {
		pricesWithFlashCrash := []float64{7.20, 7.21, 7.19, 7.22, 7.18, 7.23, 7.20, 7.19, 7.21, 7.22, 1.00, 1.10}
		fairPrice, top10 := avgTop10(pricesWithFlashCrash)

		expectedTop10 := []float64{7.18, 7.19, 7.19, 7.20, 7.20, 7.21, 7.21, 7.22, 7.22, 7.23}
		if len(top10) != len(expectedTop10) {
			t.Fatalf("异常值过滤后 Top10 长度错误: want=%d got=%d", len(expectedTop10), len(top10))
		}
		for i, want := range expectedTop10 {
			if !almostEqualFloat(top10[i], want, 1e-12) {
				t.Fatalf("异常值过滤后 Top10[%d] 错误: want=%.12f got=%.12f", i, want, top10[i])
			}
		}

		const expectedFairPrice = 7.205
		if !almostEqualFloat(fairPrice, expectedFairPrice, 1e-12) {
			t.Fatalf("异常值过滤后公允价错误: want=%.12f got=%.12f", expectedFairPrice, fairPrice)
		}

		marketFairPrice := ComputeFairPrice(pricesWithFlashCrash)
		if !almostEqualFloat(marketFairPrice, expectedFairPrice, 1e-12) {
			t.Fatalf("市场公允价应过滤插针报价: want=%.12f got=%.12f", expectedFairPrice, marketFairPrice)
		}
	})
}

func almostEqualFloat(got, want, tolerance float64) bool {
	return math.Abs(got-want) <= tolerance
}

func averageFloatSlice(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}
