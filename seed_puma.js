const BASE_URL = 'http://localhost:8081/api';

async function seedPuma() {
  try {
    const shopsRes = await fetch(`${BASE_URL}/shops`);
    const shopsData = await shopsRes.json();
    const pumaShop = shopsData.data.find(s => s.name === 'Puma');
    if (!pumaShop) {
      console.error("Puma shop not found!");
      return;
    }
    const shopId = pumaShop.id;
    console.log("Puma Shop ID:", shopId);

    // Get all products
    const prodRes = await fetch(`${BASE_URL}/products?shop_id=${shopId}`);
    const prodData = await prodRes.json();
    const pumaProds = prodData.data || [];
    console.log(`Found ${pumaProds.length} existing Puma products. Deleting them...`);

    for (const p of pumaProds) {
      await fetch(`${BASE_URL}/product/${p.id}`, { method: 'DELETE' });
    }
    console.log("Old products deleted.");

    const pumaProducts = [
      { name: "Puma Speedcat LS Red", description: "Sepatu balap ikonik dengan material suede warna merah. [Kategori: Sepatu]", price: 1899000, stock: 30, images: ["/images/puma_1.png"] },
      { name: "Puma Phase Backpack Pink", description: "Tas ransel warna pink muda dengan kapasitas besar. [Kategori: Tas]", price: 599000, stock: 45, images: ["/images/puma_2.png"] },
      { name: "Puma ESS Cap Black", description: "Topi klasik Puma dengan logo bordir 3D yang elegan. [Kategori: Aksesoris]", price: 299000, stock: 100, images: ["/images/puma_3.png"] },
      { name: "Puma Classic T-Shirt Black", description: "Kaos berbahan katun lembut dengan logo klasik Puma. [Kategori: Pakaian]", price: 399000, stock: 80, images: ["https://images.unsplash.com/photo-1521572163474-6864f9cf17ab?w=500&q=80"] }
    ];

    console.log("Adding new accurate products...");
    for (const prod of pumaProducts) {
      await fetch(`${BASE_URL}/product`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ...prod,
          shop_id: shopId
        })
      });
      console.log(`Added product: ${prod.name}`);
    }

    console.log("Puma re-seed completed!");

  } catch (err) {
    console.error("Seed error:", err);
  }
}

seedPuma();
