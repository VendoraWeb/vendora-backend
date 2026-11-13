package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"vendora/config"
	"vendora/model"

	"go.mongodb.org/mongo-driver/bson"
)

const feeRate = 0.025

func computeSubtotalNoShipping(t model.Transaction) float64 {
	if t.SubtotalAmount > 0 {
		return t.SubtotalAmount
	}
	var itemSum float64
	for _, it := range t.Items {
		itemSum += it.Price * float64(it.Quantity)
	}
	if itemSum > 0 {
		return itemSum
	}
	if t.TotalAmount > 0 {
		sub := t.TotalAmount - t.ShippingCost
		if sub > 0 {
			return sub
		}
		return t.TotalAmount
	}
	return 0
}

func runBackup(ctx context.Context) (string, error) {
	ts := time.Now().Format("20060102_150405")
	outDir := filepath.Join("backup", "dump_"+ts)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}

	cols, err := config.DB.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return "", err
	}

	for _, colName := range cols {
		cur, err := config.DB.Collection(colName).Find(ctx, bson.M{})
		if err != nil {
			return "", err
		}
		var docs []bson.M
		if err := cur.All(ctx, &docs); err != nil {
			cur.Close(ctx)
			return "", err
		}
		cur.Close(ctx)

		extJSON, err := bson.MarshalExtJSONIndent(bson.M{"collection": colName, "count": len(docs), "documents": docs}, true, true, "", "  ")
		if err != nil {
			return "", err
		}
		filePath := filepath.Join(outDir, colName+".json")
		if err := os.WriteFile(filePath, extJSON, 0644); err != nil {
			return "", err
		}
		fmt.Printf("[BACKUP] Koleksi %-16s : %d dokumen -> %s\n", colName, len(docs), filePath)
	}
	return outDir, nil
}

func main() {
	doBackup := flag.Bool("backup", true, "Buat backup seluruh koleksi MongoDB sebelum migrasi")
	apply := flag.Bool("apply", false, "Terapkan perubahan ke database (default false = dry-run saja)")
	flag.Parse()

	config.ConnectDB()
	if config.DB == nil {
		fmt.Println("Gagal terhubung ke database.")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if *doBackup {
		dir, err := runBackup(ctx)
		if err != nil {
			fmt.Printf("Backup gagal: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Backup selesai disimpan di: %s\n\n", dir)
	}

	cur, err := config.DB.Collection("transactions").Find(ctx, bson.M{})
	if err != nil {
		fmt.Printf("Gagal membaca transaksi: %v\n", err)
		os.Exit(1)
	}
	defer cur.Close(ctx)

	var txs []model.Transaction
	if err := cur.All(ctx, &txs); err != nil {
		fmt.Printf("Gagal decode transaksi: %v\n", err)
		os.Exit(1)
	}

	mode := "DRY-RUN (Tidak mengubah database)"
	if *apply {
		mode = "APPLY (Menyimpan ke database)"
	}
	fmt.Printf("=== MODE MIGRASI TRANSAKSI: %s ===\n", mode)

	needUpdate := 0
	for _, t := range txs {
		if t.SubtotalAmount > 0 && t.PlatformFee > 0 && t.EscrowStatus != "" {
			continue
		}
		sub := computeSubtotalNoShipping(t)
		tax := math.Round(sub * feeRate)
		net := sub - tax
		escrow := t.EscrowStatus
		if escrow == "" {
			switch t.Status {
			case "success":
				escrow = "released"
			case "cancelled":
				escrow = "unpaid"
			case "pending_payment":
				escrow = "unpaid"
			default:
				escrow = "held"
			}
		}
		needUpdate++
		fmt.Printf("  - TX #%s | status=%-10s | subtotal: Rp %10.0f | pajak 2.5%%: Rp %8.0f | net: Rp %10.0f | escrow: %q -> %q\n",
			t.ID.Hex()[len(t.ID.Hex())-6:], t.Status, sub, tax, net, t.EscrowStatus, escrow)

		if *apply {
			_, _ = config.DB.Collection("transactions").UpdateOne(ctx, bson.M{"_id": t.ID}, bson.M{
				"$set": bson.M{
					"subtotal_amount":   sub,
					"platform_fee":      tax,
					"seller_net_amount": net,
					"escrow_status":     escrow,
				},
			})
		}
	}
	fmt.Printf("\nTotal transaksi yang perlu dilengkapi: %d dari %d transaksi.\n", needUpdate, len(txs))
	if !*apply {
		fmt.Println("Catatan: Ini adalah DRY-RUN. Tidak ada 1 dokumen pun di database yang diubah.")
		fmt.Println("Untuk menerapkan perubahan, jalankan: go run ./cmd/migrate --apply")
	}
}
