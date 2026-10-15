package bot

import "testing"

func TestControlPoint144FXGainLoss(t *testing.T) {
	t.Run("控制点 1.4.4 功能验证", func(t *testing.T) {
		gain := calculateFXGainLoss(12000, 0, 7.28, 7.12)
		if !floatAlmostEqual(gain, 1920) {
			t.Fatalf("入款汇率损益计算错误: want=1920 got=%.8f", gain)
		}

		loss := calculateFXGainLoss(0, 4500, 7.28, 7.12)
		if !floatAlmostEqual(loss, -720) {
			t.Fatalf("下发汇率损益计算错误: want=-720 got=%.8f", loss)
		}

		flat := calculateFXGainLoss(3000, 0, 7.20, 7.20)
		if !floatAlmostEqual(flat, 0) {
			t.Fatalf("等汇率应无汇率损益: got=%.8f", flat)
		}
	})
}
