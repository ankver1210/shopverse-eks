package main

// Demo catalog loaded on first start. IDs match the stock seeded by inventory-service.
var seedProducts = []Product{
	{1, "Wireless Headphones", "Noise-cancelling, 30h battery", "Over-ear Bluetooth headphones with active noise cancellation, 30 hours of battery and a fold-flat case for travel.", "electronics", "🎧", 12999},
	{2, "Smartwatch Pro", "Heart-rate, GPS, 7-day battery", "A water-resistant smartwatch with GPS, heart-rate tracking, sleep insights and a full week of battery life.", "electronics", "⌚", 19900},
	{3, "Bluetooth Speaker", "Pocket-size, room-filling sound", "A rugged, splash-proof portable speaker with 12 hours of playtime and a built-in microphone.", "electronics", "🔊", 5999},
	{4, "Mechanical Keyboard", "Hot-swap switches, RGB", "A compact 75% mechanical keyboard with hot-swappable switches, per-key RGB and USB-C.", "electronics", "⌨️", 8950},
	{5, "Pour-Over Coffee Kit", "Dripper, kettle and filters", "Everything you need for great pour-over coffee: ceramic dripper, gooseneck kettle and 100 filters.", "home", "☕", 4499},
	{6, "Smart Desk Lamp", "Adjustable warmth, USB charging", "An LED desk lamp with adjustable colour temperature, a dimmer and a USB-C charging port in the base.", "home", "💡", 3499},
	{7, "Monstera Plant", "Easy-care indoor plant", "A healthy monstera in a 20cm pot. Thrives in indirect light and forgives the occasional missed watering.", "home", "🪴", 2400},
	{8, "Trail Backpack", "28L, weatherproof", "A lightweight 28-litre hiking backpack with a rain cover, hydration sleeve and ventilated back panel.", "outdoors", "🎒", 7900},
	{9, "2-Person Tent", "Sets up in 3 minutes", "A freestanding two-person tent with colour-coded poles, two doors and a waterproof rain fly.", "outdoors", "⛺", 14900},
	{10, "Insulated Bottle", "Cold 24h, hot 12h", "A 750ml double-walled stainless-steel bottle that keeps drinks cold for 24 hours or hot for 12.", "outdoors", "🥤", 1999},
	{11, "Pods & Pipelines", "A practical guide to Kubernetes", "A hands-on book that walks from your first Pod to production pipelines, autoscaling and observability.", "books", "📘", 3499},
	{12, "The Little Book of YAML", "Indentation, demystified", "A short, friendly guide to YAML for engineers who write Kubernetes manifests every day.", "books", "📙", 1499},
}
