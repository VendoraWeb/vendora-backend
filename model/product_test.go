package model

import "testing"

func TestProductEffectivePrice(t *testing.T) {
	cases := []struct {
		name string
		prod Product
		want float64
	}{
		{"harga normal tanpa promo", Product{Price: 1500000, PromoPrice: 0}, 1500000},
		{"harga promo valid", Product{Price: 1500000, PromoPrice: 1200000}, 1200000},
		{"promo sama dengan harga normal diabaikan", Product{Price: 1500000, PromoPrice: 1500000}, 1500000},
		{"promo lebih mahal dari normal diabaikan", Product{Price: 1500000, PromoPrice: 1750000}, 1500000},
		{"promo negatif diabaikan", Product{Price: 1500000, PromoPrice: -50000}, 1500000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.prod.EffectivePrice(); got != tc.want {
				t.Errorf("EffectivePrice() = %.0f, want %.0f", got, tc.want)
			}
		})
	}
}
