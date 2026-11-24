package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type TransactionItem struct {
	ProductID     primitive.ObjectID `bson:"product_id" json:"product_id"`
	ShopID        primitive.ObjectID `bson:"shop_id,omitempty" json:"shop_id,omitempty"`
	Name          string             `bson:"name" json:"name"`
	Variant       string             `bson:"variant,omitempty" json:"variant,omitempty"` // mis. "Burgundy, 512 GB"
	Image         string             `bson:"image,omitempty" json:"image,omitempty"`
	OriginalPrice float64            `bson:"original_price,omitempty" json:"original_price,omitempty"`
	Price         float64            `bson:"price" json:"price"`
	Quantity      int                `bson:"quantity" json:"quantity"`
}

// GeoLocation merepresentasikan titik koordinat di peta pelacakan pesanan.
type GeoLocation struct {
	Label string  `bson:"label" json:"label"`
	City  string  `bson:"city,omitempty" json:"city,omitempty"`
	Lat   float64 `bson:"lat" json:"lat"`
	Lng   float64 `bson:"lng" json:"lng"`
}

// TrackingEvent adalah satu riwayat perjalanan paket dari kurir.
type TrackingEvent struct {
	Status      string             `bson:"status" json:"status"`           // manifested, in_transit, out_for_delivery, delivered
	Title       string             `bson:"title" json:"title"`             // mis. "Paket telah diserahkan ke kurir JNE"
	Description string             `bson:"description" json:"description"` // detail lokasi hub
	City        string             `bson:"city" json:"city"`
	Lat         float64            `bson:"lat" json:"lat"`
	Lng         float64            `bson:"lng" json:"lng"`
	Timestamp   primitive.DateTime `bson:"timestamp" json:"timestamp"`
}

type Transaction struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	BuyerID         primitive.ObjectID `bson:"buyer_id" json:"buyer_id"` // References User (role: buyer)
	RecipientName   string             `bson:"recipient_name" json:"recipient_name"`
	ShippingAddress string             `bson:"shipping_address" json:"shipping_address"`
	Items           []TransactionItem  `bson:"items" json:"items"`
	ShopID          primitive.ObjectID `bson:"shop_id,omitempty" json:"shop_id,omitempty"` // satu pesanan = satu ruko

	// Rincian Keuangan & Escrow (Rekening Bersama Admin)
	SubtotalAmount  float64             `bson:"subtotal_amount" json:"subtotal_amount"`             // Total harga barang
	ShippingCost    float64             `bson:"shipping_cost" json:"shipping_cost"`                 // Ongkos kirim kurir
	PlatformFee     float64             `bson:"platform_fee" json:"platform_fee"`                   // Pajak/biaya layanan platform (2.5%)
	SellerNetAmount float64             `bson:"seller_net_amount" json:"seller_net_amount"`         // Dana bersih yang cair ke penjual setelah pesanan diterima
	TotalAmount     float64             `bson:"total_amount" json:"total_amount"`                   // Total yang dibayar pembeli (Subtotal + Ongkir)
	EscrowStatus    string              `bson:"escrow_status" json:"escrow_status"`                 // unpaid, held (ditahan di Admin), released (cair ke Seller), refunded (kembali ke Buyer)
	ReleasedAt      *primitive.DateTime `bson:"released_at,omitempty" json:"released_at,omitempty"` // Waktu dana cair ke penjual

	// Pembayaran Real-Time & Keamanan
	PaymentMethod    string              `bson:"payment_method,omitempty" json:"payment_method,omitempty"`         // QRIS, VA_BCA, VA_MANDIRI, CARD, EWALLET
	PaymentCode      string              `bson:"payment_code,omitempty" json:"payment_code,omitempty"`             // Nomor VA / QR String
	PaymentToken     string              `bson:"payment_token,omitempty" json:"payment_token,omitempty"`           // Signature keamanan transaksi
	PaymentExpiresAt *primitive.DateTime `bson:"payment_expires_at,omitempty" json:"payment_expires_at,omitempty"` // Batas waktu bayar (15 menit)
	PaidAt           *primitive.DateTime `bson:"paid_at,omitempty" json:"paid_at,omitempty"`

	// Kurir & Pelacakan Peta (Map Tracking)
	CourierCode      string          `bson:"courier_code,omitempty" json:"courier_code,omitempty"`           // JNE, JNT, SICEPAT, INSTANT
	CourierService   string          `bson:"courier_service,omitempty" json:"courier_service,omitempty"`     // mis. "JNE Reguler (2-3 Hari)"
	CourierETD       string          `bson:"courier_etd,omitempty" json:"courier_etd,omitempty"`             // Estimasi tiba
	TrackingNumber   string          `bson:"tracking_number,omitempty" json:"tracking_number,omitempty"`     // Nomor resi
	RouteOrigin      *GeoLocation    `bson:"route_origin,omitempty" json:"route_origin,omitempty"`           // Titik koordinat toko pengirim
	RouteDestination *GeoLocation    `bson:"route_destination,omitempty" json:"route_destination,omitempty"` // Titik koordinat alamat pembeli
	CurrentLocation  *GeoLocation    `bson:"current_location,omitempty" json:"current_location,omitempty"`   // Posisi paket saat ini di peta
	TrackingHistory  []TrackingEvent `bson:"tracking_history,omitempty" json:"tracking_history,omitempty"`   // Timeline perjalanan kurir

	Status string `bson:"status" json:"status"` // pending_payment, pending, paid, shipped, success, cancelled, disputed
	// Batas waktu penjual wajib mengonfirmasi pesanan baru (default 24 jam). Hanya diisi untuk pesanan baru agar data historis tidak dibatalkan otomatis saat startup.
	ConfirmBy *primitive.DateTime `bson:"confirm_by,omitempty" json:"confirm_by,omitempty"`
	// Batas waktu pesanan otomatis selesai (dana cair) bila pembeli tidak konfirmasi & tidak komplain.
	ShippedAt      *primitive.DateTime `bson:"shipped_at,omitempty" json:"shipped_at,omitempty"`
	AutoCompleteAt *primitive.DateTime `bson:"auto_complete_at,omitempty" json:"auto_complete_at,omitempty"`
	AutoCompleted  bool                `bson:"auto_completed,omitempty" json:"auto_completed,omitempty"`
	CreatedAt      primitive.DateTime  `bson:"created_at" json:"created_at"`
}

// Dispute adalah komplain/retur pembeli. Selama sengketa terbuka, dana tetap di Escrow
// dan pesanan tidak akan selesai otomatis. Admin memutuskan refund atau teruskan dana.
type Dispute struct {
	ID             primitive.ObjectID   `bson:"_id,omitempty" json:"id,omitempty"`
	TransactionID  primitive.ObjectID   `bson:"transaction_id" json:"transaction_id"`
	BuyerID        primitive.ObjectID   `bson:"buyer_id" json:"buyer_id"`
	ShopIDs        []primitive.ObjectID `bson:"shop_ids" json:"shop_ids"`
	Reason         string               `bson:"reason" json:"reason"` // not_received, not_as_described, damaged, other
	Description    string               `bson:"description" json:"description"`
	Evidence       []string             `bson:"evidence,omitempty" json:"evidence,omitempty"` // URL foto bukti
	SellerResponse string               `bson:"seller_response,omitempty" json:"seller_response,omitempty"`
	Status         string               `bson:"status" json:"status"` // open, resolved_refund, resolved_release
	AdminNote      string               `bson:"admin_note,omitempty" json:"admin_note,omitempty"`
	Amount         float64              `bson:"amount" json:"amount"`
	CreatedAt      primitive.DateTime   `bson:"created_at" json:"created_at"`
	ResolvedAt     *primitive.DateTime  `bson:"resolved_at,omitempty" json:"resolved_at,omitempty"`
}

// WalletLedger mencatat setiap pergerakan uang (Escrow masuk, Pajak Admin, Pencairan ke Penjual, Penarikan Saldo).
type WalletLedger struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	TransactionID primitive.ObjectID `bson:"transaction_id,omitempty" json:"transaction_id,omitempty"`
	ShopID        primitive.ObjectID `bson:"shop_id,omitempty" json:"shop_id,omitempty"`
	Type          string             `bson:"type" json:"type"` // escrow_hold, escrow_release, platform_tax, escrow_refund, withdrawal
	GrossAmount   float64            `bson:"gross_amount" json:"gross_amount"`
	TaxAmount     float64            `bson:"tax_amount" json:"tax_amount"`
	NetAmount     float64            `bson:"net_amount" json:"net_amount"`
	BankName      string             `bson:"bank_name,omitempty" json:"bank_name,omitempty"`
	AccountNumber string             `bson:"account_number,omitempty" json:"account_number,omitempty"`
	Description   string             `bson:"description" json:"description"`
	CreatedAt     primitive.DateTime `bson:"created_at" json:"created_at"`
}
