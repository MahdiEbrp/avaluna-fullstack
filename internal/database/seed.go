package database

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/config"
)

func (d *DB) Seeded(ctx context.Context) bool {
	n, err := d.Count(ctx, `SELECT COUNT(*) FROM products`)
	return err == nil && n > 0
}

func Seed(ctx context.Context, d *DB, cfg *config.Config) error {
	rnd := rand.New(rand.NewSource(20260101))

	steps := []struct {
		name string
		fn   func(context.Context, *DB, *config.Config, *rand.Rand) error
	}{
		{"settings", seedSettings},
		{"currencies", seedCurrencies},
		{"users", seedUsers},
		{"cms", seedCMS},
		{"taxonomy", seedTaxonomy},
		{"products", seedProducts},
		{"inventory", seedInventory},
		{"commerce-config", seedCommerceConfig},
		{"vendors", seedVendors},
		{"blog", seedBlog},
		{"orders", seedOrders},
		{"reviews", seedReviews},
		{"engagement", seedEngagement},
		{"ops", seedOps},
		{"cron", seedCronJobs},
	}
	for _, s := range steps {
		if err := s.fn(ctx, d, cfg, rnd); err != nil {
			return fmt.Errorf("seed %s: %w", s.name, err)
		}
	}
	return nil
}

func set(ctx context.Context, d *DB, key, val, scope string) {
	_, _ = d.Write(ctx,
		`INSERT INTO settings(key,value,scope) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, scope=excluded.scope`, key, val, scope)
}

func seedSettings(ctx context.Context, d *DB, cfg *config.Config, _ *rand.Rand) error {
	pub := map[string]string{
		"site_name":          "فروشگاه آرنا",
		"site_name_en":       "Arena Store",
		"site_tagline":       "همه‌چیز برای زندگی دیجیتال شما",
		"site_tagline_en":    "Everything for your digital life",
		"site_url":           cfg.BaseURL,
		"support_email":      "support@arenastore.example",
		"support_phone":      "+98 21 1234 5678",
		"address":            "تهران، خیابان ولیعصر، پلاک ۱۲۰",
		"default_currency":   cfg.DefaultCurrency,
		"default_locale":     cfg.DefaultLocale,
		"free_shipping_over": "5000000",
		"low_stock_warn":     "5",
		"instagram":          "https://instagram.com/arenastore",
		"telegram":           "https://t.me/arenastore",
	}
	for k, v := range pub {
		set(ctx, d, k, v, "public")
	}
	priv := map[string]string{
		"maintenance_mode":                 "false",
		"maintenance_message":              "به‌زودی بازمی‌گردیم / We will be back shortly.",
		"unpaid_order_window_minutes":      "120",
		"abandoned_cart_hours":             "6",
		"loyalty_points_per_unit":          "10000",
		"loyalty_value_per_point":          "1000",
		"loyalty_min_redeem":               "50",
		"loyalty_expiry_days":              "365",
		"referral_reward":                  "50000",
		"giftcard_min_amount":              "100000",
		"backup_keep":                      "7",
		"backup_encrypt":                   "true",
		"log_retention_days":               "90",
		"fraud_hourly_volume_threshold":    "500000000",
		"subscription_max_failures":        "3",
		"webhook_allow_unsigned":           "true",
		"webhook_secret":                   auth.RandomToken(24),
		"vendor_min_payout":                "1000000",
		"review_auto_approve":              "false",
		"license_expires_at":               time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02"),
		"tax_inclusive_prices":             "false",
		"order_number_prefix":              "ORD",
		"search_suggestions_limit":         "8",
		"analytics_retention_days":         "180",
		"password_min_length":              "8",
		"session_idle_timeout_minutes":     "60",
		"twofa_required_for_admin":         "true",
		"idempotency_required_for_payment": "true",
	}
	for k, v := range priv {
		set(ctx, d, k, v, "private")
	}
	return nil
}

func seedCurrencies(ctx context.Context, d *DB, _ *config.Config, _ *rand.Rand) error {
	rows := [][]any{
		{"IRR", "ریال ایران", "﷼", 1.0, 0, 1, 1},
		{"IRT", "تومان", "تومان", 0.1, 0, 1, 0},
		{"USD", "US Dollar", "$", 0.0000019, 2, 1, 0},
		{"EUR", "Euro", "€", 0.0000017, 2, 1, 0},
		{"AED", "UAE Dirham", "د.إ", 0.0000069, 2, 1, 0},
	}
	for _, r := range rows {
		if _, err := d.Write(ctx,
			`INSERT INTO currencies(code,name,symbol,rate,decimals,active,is_base) VALUES(?,?,?,?,?,?,?)
			 ON CONFLICT(code) DO UPDATE SET rate=excluded.rate, symbol=excluded.symbol`, r...); err != nil {
			return err
		}
	}
	return nil
}

type seedUser struct {
	Email, First, Last, Role, Phone, Pass string
	Wallet                                int
}

func seedUsers(ctx context.Context, d *DB, cfg *config.Config, _ *rand.Rand) error {
	users := []seedUser{
		{cfg.AdminEmail, "مدیر", "سیستم", auth.RoleSuperAdmin, "+989120000001", cfg.AdminPass, 0},
		{"manager@yourstore.com", "سارا", "محمدی", auth.RoleAdmin, "+989120000002", "Admin@12345", 0},
		{"editor@yourstore.com", "نیما", "رضایی", auth.RoleEditor, "+989120000003", "Editor@12345", 0},
		{"support@yourstore.com", "لیلا", "کریمی", auth.RoleSupport, "+989120000004", "Support@12345", 0},
		{"vendor1@yourstore.com", "حسین", "اکبری", auth.RoleVendor, "+989120000005", "Vendor@12345", 0},
		{"vendor2@yourstore.com", "مریم", "حسینی", auth.RoleVendor, "+989120000006", "Vendor@12345", 0},
		{"ali@example.com", "علی", "تهرانی", auth.RoleCustomer, "+989121111111", "Customer@12345", 25000000},
		{"zahra@example.com", "زهرا", "نوری", auth.RoleCustomer, "+989121111112", "Customer@12345", 12000000},
		{"reza@example.com", "رضا", "صادقی", auth.RoleCustomer, "+989121111113", "Customer@12345", 8000000},
		{"john@example.com", "John", "Carter", auth.RoleCustomer, "+14155550101", "Customer@12345", 5000000},
		{"emma@example.com", "Emma", "Blake", auth.RoleCustomer, "+14155550102", "Customer@12345", 3000000},
	}
	for i, u := range users {
		hash, err := auth.HashPassword(u.Pass, cfg.ArgonPepper)
		if err != nil {
			return err
		}
		locale := "fa"
		if strings.Contains(u.Email, "john") || strings.Contains(u.Email, "emma") {
			locale = "en"
		}
		res, err := d.Write(ctx, `
			INSERT INTO users(uuid,email,phone,password_hash,first_name,last_name,role,status,
				email_verified_at,locale,currency,referral_code)
			VALUES(?,?,?,?,?,?,?,'active',datetime('now'),?,?,?)
			ON CONFLICT(email) DO UPDATE SET role=excluded.role, status='active'`,
			auth.RandomToken(16), u.Email, u.Phone, hash, u.First, u.Last, u.Role, locale,
			"IRR", fmt.Sprintf("REF%04d", 1000+i))
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if id == 0 {
			_ = d.Row(ctx, `SELECT id FROM users WHERE email=?`, []any{u.Email}, &id)
		}
		_, _ = d.Write(ctx, `INSERT OR IGNORE INTO wallets(user_id,balance) VALUES(?,?)`, id, u.Wallet)
		_, _ = d.Write(ctx, `INSERT OR IGNORE INTO notification_preferences(user_id) VALUES(?)`, id)
		_, _ = d.Write(ctx, `INSERT OR IGNORE INTO loyalty_accounts(user_id) VALUES(?)`, id)
		if u.Role == auth.RoleCustomer {
			_, _ = d.Write(ctx, `INSERT INTO addresses(user_id,label,recipient,phone,country,province,city,postal_code,line1,is_default)
				SELECT ?,?,?,?,?,?,?,?,?,1
				WHERE NOT EXISTS (SELECT 1 FROM addresses WHERE user_id=?)`,
				id, "home", u.First+" "+u.Last, u.Phone, "IR", "تهران", "تهران", "1467983754",
				"خیابان ولیعصر، کوچه بهار، پلاک ۱۲", id)
		}
	}
	return nil
}

func seedCMS(ctx context.Context, d *DB, _ *config.Config, _ *rand.Rand) error {
	pages := [][]any{
		{"about", "درباره ما", "About Us",
			"<p>فروشگاه آرنا از سال ۱۳۹۸ به ارائه محصولات دیجیتال اصل با گارانتی معتبر مشغول است.</p>",
			"<p>Arena Store has been delivering authentic digital products with genuine warranty since 2019.</p>"},
		{"contact", "تماس با ما", "Contact Us",
			"<p>تلفن: ۰۲۱-۱۲۳۴۵۶۷۸ — ایمیل: support@arenastore.example</p>",
			"<p>Phone: +98 21 1234 5678 — Email: support@arenastore.example</p>"},
		{"terms", "شرایط و قوانین", "Terms & Conditions",
			"<p>استفاده از این وب‌سایت به معنی پذیرش کامل قوانین آن است.</p>",
			"<p>By using this website you accept these terms in full.</p>"},
		{"privacy", "حریم خصوصی", "Privacy Policy",
			"<p>اطلاعات شما نزد ما محفوظ است و با هیچ شخص ثالثی به اشتراک گذاشته نمی‌شود.</p>",
			"<p>Your data stays with us and is never shared with third parties.</p>"},
		{"shipping-policy", "راهنمای ارسال", "Shipping Policy",
			"<p>ارسال به سراسر ایران بین ۱ تا ۵ روز کاری انجام می‌شود.</p>",
			"<p>Nationwide delivery takes between 1 and 5 business days.</p>"},
		{"returns-policy", "رویه مرجوعی", "Returns Policy",
			"<p>تا ۷ روز پس از دریافت کالا امکان مرجوع‌کردن وجود دارد.</p>",
			"<p>You may return any item within 7 days of delivery.</p>"},
	}
	for _, p := range pages {
		if _, err := d.Write(ctx,
			`INSERT INTO pages(slug,title,title_en,body,body_en,status) VALUES(?,?,?,?,?,'published')
			 ON CONFLICT(slug) DO UPDATE SET title=excluded.title, body=excluded.body`, p...); err != nil {
			return err
		}
	}

	menus := [][]any{
		{"main", "منوی اصلی", `[
			{"label":"خانه","labelEn":"Home","url":"/"},
			{"label":"فروشگاه","labelEn":"Shop","url":"/shop"},
			{"label":"وبلاگ","labelEn":"Blog","url":"/blog"},
			{"label":"پرسش‌های متداول","labelEn":"FAQ","url":"/faq"},
			{"label":"تماس با ما","labelEn":"Contact","url":"/page/contact"}
		]`},
		{"footer", "منوی پاورقی", `[
			{"label":"درباره ما","labelEn":"About","url":"/page/about"},
			{"label":"شرایط و قوانین","labelEn":"Terms","url":"/page/terms"},
			{"label":"حریم خصوصی","labelEn":"Privacy","url":"/page/privacy"},
			{"label":"راهنمای ارسال","labelEn":"Shipping","url":"/page/shipping-policy"},
			{"label":"رویه مرجوعی","labelEn":"Returns","url":"/page/returns-policy"}
		]`},
	}
	for _, m := range menus {
		if _, err := d.Write(ctx,
			`INSERT INTO menus(code,name,items_json) VALUES(?,?,?)
			 ON CONFLICT(code) DO UPDATE SET items_json=excluded.items_json`, m...); err != nil {
			return err
		}
	}

	banners := [][]any{
		{"جشنواره پاییزی", "Autumn Festival", "تا ۴۰٪ تخفیف روی لپ‌تاپ و هدفون",
			"Up to 40% off laptops and headphones", "/shop?sale=1", "hero", 1},
		{"ارسال رایگان", "Free Shipping", "برای سفارش‌های بالای ۵۰۰ هزار تومان",
			"On orders above 5,000,000 IRR", "/page/shipping-policy", "hero", 2},
		{"گارانتی ۱۸ ماهه", "18-Month Warranty", "روی تمام کالاهای دیجیتال",
			"On every digital product", "/page/terms", "strip", 3},
	}
	for _, b := range banners {
		if _, err := d.Write(ctx, `INSERT INTO banners(title,title_en,subtitle,subtitle_en,link,position,sort_order,active)
			SELECT ?,?,?,?,?,?,?,1 WHERE NOT EXISTS (SELECT 1 FROM banners WHERE title=?)`,
			b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[0]); err != nil {
			return err
		}
	}

	faqs := [][]any{
		{"چطور سفارشم را پیگیری کنم؟", "How do I track my order?",
			"از بخش «حساب من ← سفارش‌ها» کد رهگیری را مشاهده کنید.",
			"Open My Account → Orders to find your tracking code.", "orders"},
		{"روش‌های پرداخت کدام‌اند؟", "Which payment methods do you accept?",
			"زرین‌پال، استرایپ (سندباکس) و کیف پول داخلی.",
			"ZarinPal, Stripe (sandbox) and the built-in wallet.", "payments"},
		{"آیا امکان مرجوع‌کردن کالا هست؟", "Can I return an item?",
			"بله، تا ۷ روز پس از دریافت کالا.", "Yes, within 7 days of delivery.", "returns"},
		{"هزینه ارسال چقدر است؟", "How much is shipping?",
			"بر اساس وزن و روش ارسال محاسبه می‌شود؛ بالای ۵۰۰ هزار تومان رایگان است.",
			"Calculated by weight and method; free above 5,000,000 IRR.", "shipping"},
		{"چگونه امتیاز وفاداری بگیرم؟", "How do I earn loyalty points?",
			"به ازای هر ۱۰۰۰ تومان خرید یک امتیاز دریافت می‌کنید.",
			"You earn one point for every 10,000 IRR spent.", "loyalty"},
	}
	for i, f := range faqs {
		if _, err := d.Write(ctx, `INSERT INTO faqs(question,question_en,answer,answer_en,category,sort_order,active)
			SELECT ?,?,?,?,?,?,1 WHERE NOT EXISTS (SELECT 1 FROM faqs WHERE question=?)`,
			f[0], f[1], f[2], f[3], f[4], i, f[0]); err != nil {
			return err
		}
	}
	return nil
}

func seedTaxonomy(ctx context.Context, d *DB, _ *config.Config, _ *rand.Rand) error {
	type cat struct{ Slug, Fa, En, Parent, Icon string }
	cats := []cat{
		{"digital", "کالای دیجیتال", "Digital", "", "💻"},
		{"laptops", "لپ‌تاپ", "Laptops", "digital", "💻"},
		{"phones", "گوشی موبایل", "Mobile Phones", "digital", "📱"},
		{"audio", "صوتی و هدفون", "Audio & Headphones", "digital", "🎧"},
		{"wearables", "پوشیدنی", "Wearables", "digital", "⌚"},
		{"home", "خانه و آشپزخانه", "Home & Kitchen", "", "🏠"},
		{"appliances", "لوازم خانگی", "Appliances", "home", "🧺"},
		{"accessories", "لوازم جانبی", "Accessories", "", "🔌"},
		{"gaming", "گیمینگ", "Gaming", "", "🎮"},
	}
	ids := map[string]int64{}
	for i, c := range cats {
		var parent any
		if c.Parent != "" {
			parent = ids[c.Parent]
		}
		res, err := d.Write(ctx,
			`INSERT INTO categories(slug,name,name_en,parent_id,icon,sort_order,active) VALUES(?,?,?,?,?,?,1)
			 ON CONFLICT(slug) DO UPDATE SET name=excluded.name, name_en=excluded.name_en`,
			c.Slug, c.Fa, c.En, parent, c.Icon, i)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if id == 0 {
			_ = d.Row(ctx, `SELECT id FROM categories WHERE slug=?`, []any{c.Slug}, &id)
		}
		ids[c.Slug] = id
	}

	brands := [][]any{
		{"apple", "اپل", "Apple", "US"}, {"samsung", "سامسونگ", "Samsung", "KR"},
		{"sony", "سونی", "Sony", "JP"}, {"asus", "ایسوس", "ASUS", "TW"},
		{"xiaomi", "شیائومی", "Xiaomi", "CN"}, {"logitech", "لاجیتک", "Logitech", "CH"},
		{"anker", "انکر", "Anker", "CN"}, {"bosch", "بوش", "Bosch", "DE"},
	}
	for _, b := range brands {
		if _, err := d.Write(ctx,
			`INSERT INTO brands(slug,name,name_en,country,active) VALUES(?,?,?,?,1)
			 ON CONFLICT(slug) DO UPDATE SET name=excluded.name`, b...); err != nil {
			return err
		}
	}

	attrs := [][]any{
		{"color", "رنگ", "Color", "color", `["مشکی","سفید","نقره‌ای","آبی","قرمز"]`},
		{"size", "اندازه", "Size", "select", `["S","M","L","XL"]`},
		{"storage", "حافظه", "Storage", "select", `["128GB","256GB","512GB","1TB"]`},
		{"ram", "رم", "RAM", "select", `["8GB","16GB","32GB"]`},
		{"warranty", "گارانتی", "Warranty", "select", `["12 ماه","18 ماه","24 ماه"]`},
	}
	for _, a := range attrs {
		if _, err := d.Write(ctx,
			`INSERT INTO attributes(code,name,name_en,type,values_json,filterable) VALUES(?,?,?,?,?,1)
			 ON CONFLICT(code) DO UPDATE SET values_json=excluded.values_json`, a...); err != nil {
			return err
		}
	}

	warehouses := [][]any{
		{"THR", "انبار مرکزی تهران", "تهران"},
		{"MSH", "انبار مشهد", "مشهد"},
		{"SHZ", "انبار شیراز", "شیراز"},
	}
	for _, wdata := range warehouses {
		if _, err := d.Write(ctx,
			`INSERT INTO warehouses(code,name,city,active) VALUES(?,?,?,1)
			 ON CONFLICT(code) DO UPDATE SET name=excluded.name`, wdata...); err != nil {
			return err
		}
	}
	return nil
}

type seedProduct struct {
	SKU, Slug, Fa, En, Cat, Brand string
	Price, Compare, Cost, Weight  int
	Featured                      bool
	DescFa, DescEn                string
}

func catalogue() []seedProduct {
	return []seedProduct{
		{"LPT-MBA-13", "macbook-air-m3", "مک‌بوک ایر M3 ۱۳ اینچ", "MacBook Air M3 13\"", "laptops", "apple", 920000000, 1050000000, 760000000, 1240, true, "نازک، سبک و بی‌صدا با تراشه M3 و ۱۸ ساعت باتری.", "Thin, light and silent with the M3 chip and 18-hour battery."},
		{"LPT-ZEN-14", "asus-zenbook-14", "ایسوس زن‌بوک ۱۴", "ASUS Zenbook 14 OLED", "laptops", "asus", 610000000, 690000000, 505000000, 1390, true, "نمایشگر OLED دو‌و‌نیم‌کی با بدنه آلومینیومی.", "2.8K OLED panel in an all-aluminium chassis."},
		{"LPT-ROG-16", "asus-rog-strix-16", "ایسوس ROG استریکس ۱۶", "ASUS ROG Strix 16", "gaming", "asus", 1480000000, 1690000000, 1250000000, 2500, true, "لپ‌تاپ گیمینگ با RTX و نمایشگر ۲۴۰ هرتز.", "RTX-powered gaming laptop with a 240Hz display."},
		{"PHN-IP15-128", "iphone-15-128", "آیفون ۱۵ ظرفیت ۱۲۸", "iPhone 15 128GB", "phones", "apple", 1050000000, 1180000000, 890000000, 171, true, "دوربین ۴۸ مگاپیکسلی و پورت USB-C.", "48MP main camera and USB-C."},
		{"PHN-S24-256", "galaxy-s24-256", "گلکسی S24 ظرفیت ۲۵۶", "Galaxy S24 256GB", "phones", "samsung", 880000000, 960000000, 740000000, 167, true, "نمایشگر داینامیک AMOLED 2X با نرخ ۱۲۰ هرتز.", "Dynamic AMOLED 2X at 120Hz."},
		{"PHN-RN13", "redmi-note-13", "ردمی نوت ۱۳", "Redmi Note 13", "phones", "xiaomi", 185000000, 215000000, 148000000, 188, false, "باتری ۵۰۰۰ میلی‌آمپری با شارژ سریع.", "5000mAh battery with fast charging."},
		{"AUD-WH1000", "sony-wh1000xm5", "هدفون سونی WH-1000XM5", "Sony WH-1000XM5", "audio", "sony", 295000000, 340000000, 235000000, 250, true, "نویز کنسلینگ مرجع بازار با ۳۰ ساعت پخش.", "Class-leading noise cancelling, 30h playback."},
		{"AUD-APP2", "airpods-pro-2", "ایرپاد پرو نسل ۲", "AirPods Pro (2nd gen)", "audio", "apple", 245000000, 270000000, 198000000, 61, true, "حالت شفافیت تطبیقی و کیس USB-C.", "Adaptive transparency and a USB-C case."},
		{"AUD-SBH-X", "anker-soundcore-x", "انکر ساندکور اسپیس X", "Anker Soundcore Space X", "audio", "anker", 78000000, 95000000, 58000000, 290, false, "صدای فراگیر با ۶۰ ساعت باتری.", "Spatial audio with a 60-hour battery."},
		{"WER-AW9", "apple-watch-9", "اپل واچ سری ۹", "Apple Watch Series 9", "wearables", "apple", 420000000, 470000000, 350000000, 51, true, "حسگر اکسیژن خون و نمایشگر همیشه روشن.", "Blood-oxygen sensor with always-on display."},
		{"WER-GW6", "galaxy-watch-6", "گلکسی واچ ۶", "Galaxy Watch 6", "wearables", "samsung", 265000000, 310000000, 215000000, 59, false, "پایش خواب پیشرفته و بدنه سبک.", "Advanced sleep tracking in a light body."},
		{"WER-MIB8", "mi-band-8", "می بند ۸", "Xiaomi Smart Band 8", "wearables", "xiaomi", 38000000, 46000000, 27000000, 27, false, "۱۶ روز باتری و ۱۵۰ حالت ورزشی.", "16-day battery and 150 workout modes."},
		{"ACC-MX3S", "logitech-mx-master-3s", "موس لاجیتک MX Master 3S", "Logitech MX Master 3S", "accessories", "logitech", 72000000, 85000000, 54000000, 141, true, "اسکرول مگنتیک و کلیک بی‌صدا.", "MagSpeed scrolling and quiet clicks."},
		{"ACC-MXKEY", "logitech-mx-keys-s", "کیبورد لاجیتک MX Keys S", "Logitech MX Keys S", "accessories", "logitech", 89000000, 105000000, 68000000, 810, false, "بک‌لایت هوشمند و اتصال سه‌دستگاهه.", "Smart backlight, three-device switching."},
		{"ACC-PB737", "anker-powerbank-737", "پاوربانک انکر ۷۳۷", "Anker PowerCore 737", "accessories", "anker", 96000000, 112000000, 74000000, 630, false, "۲۴۰۰۰ میلی‌آمپر با خروجی ۱۴۰ وات.", "24,000mAh with 140W output."},
		{"ACC-USBC-HUB", "anker-usbc-hub-11", "هاب انکر USB-C یازده‌پورت", "Anker 11-in-1 USB-C Hub", "accessories", "anker", 64000000, 78000000, 46000000, 210, false, "خروجی ۴K و پورت اترنت گیگابیتی.", "4K output and gigabit Ethernet."},
		{"GAM-PS5", "playstation-5-slim", "پلی‌استیشن ۵ اسلیم", "PlayStation 5 Slim", "gaming", "sony", 1180000000, 1290000000, 1010000000, 3200, true, "درایو نوری جداشدنی و SSD یک‌ترابایتی.", "Detachable disc drive and 1TB SSD."},
		{"GAM-DS-EDGE", "dualsense-edge", "دسته دوال‌سنس اج", "DualSense Edge Controller", "gaming", "sony", 215000000, 240000000, 176000000, 325, false, "استیک‌های قابل تعویض و پروفایل سفارشی.", "Swappable sticks and custom profiles."},
		{"GAM-HS-G733", "logitech-g733", "هدست گیمینگ G733", "Logitech G733 Headset", "gaming", "logitech", 88000000, 99000000, 66000000, 278, false, "بی‌سیم LIGHTSPEED با نورپردازی RGB.", "LIGHTSPEED wireless with RGB lighting."},
		{"HOM-BOS-CM", "bosch-coffee-maker", "قهوه‌ساز بوش", "Bosch Coffee Maker", "appliances", "bosch", 142000000, 168000000, 112000000, 4100, false, "دمنوش‌ساز خودکار با فیلتر دائمی.", "Automatic brewer with a permanent filter."},
		{"HOM-BOS-VAC", "bosch-vacuum-series6", "جاروبرقی بوش سری ۶", "Bosch Vacuum Series 6", "appliances", "bosch", 268000000, 310000000, 218000000, 7200, false, "فیلتر HEPA و موتور کم‌صدا.", "HEPA filtration and a quiet motor."},
		{"HOM-XM-AIR", "xiaomi-air-purifier-4", "تصفیه هوا شیائومی ۴", "Xiaomi Air Purifier 4", "appliances", "xiaomi", 118000000, 139000000, 92000000, 5600, false, "پوشش ۴۸ متر مربع با نمایشگر OLED.", "48 m² coverage with an OLED display."},
		{"HOM-SAM-MW", "samsung-microwave-23", "مایکروویو سامسونگ ۲۳ لیتری", "Samsung Microwave 23L", "appliances", "samsung", 156000000, 180000000, 124000000, 12500, false, "پوشش سرامیکی و حالت دیفراست هوشمند.", "Ceramic interior and smart defrost."},
		{"ACC-APL-PEN", "apple-pencil-pro", "اپل پنسل پرو", "Apple Pencil Pro", "accessories", "apple", 125000000, 140000000, 98000000, 19, false, "حساس به فشار با حالت فشردن.", "Pressure-sensitive with squeeze gestures."},
	}
}

func seedProducts(ctx context.Context, d *DB, _ *config.Config, rnd *rand.Rand) error {
	for _, p := range catalogue() {
		var catID, brandID int64
		_ = d.Row(ctx, `SELECT id FROM categories WHERE slug=?`, []any{p.Cat}, &catID)
		_ = d.Row(ctx, `SELECT id FROM brands WHERE slug=?`, []any{p.Brand}, &brandID)
		featured := 0
		if p.Featured {
			featured = 1
		}
		res, err := d.Write(ctx, `
			INSERT INTO products(slug,sku,name,name_en,short_desc,short_desc_en,description,description_en,
				category_id,brand_id,price,compare_price,cost_price,currency,weight_g,status,featured,
				thumbnail,gallery_json,attrs_json,sold_count,view_count,published_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?, 'IRR',?, 'published',?,?,?,?,?,?,datetime('now','-30 days'))
			ON CONFLICT(sku) DO UPDATE SET price=excluded.price, compare_price=excluded.compare_price`,
			p.Slug, p.SKU, p.Fa, p.En, p.DescFa, p.DescEn, p.DescFa, p.DescEn,
			catID, brandID, p.Price, p.Compare, p.Cost, p.Weight, featured,
			placeholderImage(p.Slug), fmt.Sprintf(`["%s"]`, placeholderImage(p.Slug)),
			`{"warranty":"18 ماه","origin":"اصل"}`, rnd.Intn(180), rnd.Intn(4000))
		if err != nil {
			return err
		}
		pid, _ := res.LastInsertId()
		if pid == 0 {
			_ = d.Row(ctx, `SELECT id FROM products WHERE sku=?`, []any{p.SKU}, &pid)
		}
		variants := variantsFor(p)
		for _, v := range variants {
			if _, err := d.Write(ctx, `
				INSERT INTO product_variants(product_id,sku,title,options_json,price,compare_price,weight_g,active)
				VALUES(?,?,?,?,?,?,?,1)
				ON CONFLICT(sku) DO UPDATE SET price=excluded.price`,
				pid, v.sku, v.title, v.opts, v.price, p.Compare, p.Weight); err != nil {
				return err
			}
		}
	}

	if _, err := d.Write(ctx, `DELETE FROM products_fts`); err != nil {
		return err
	}
	_, err := d.Write(ctx, `INSERT INTO products_fts(name,name_en,short_desc,description,sku)
		SELECT name,name_en,short_desc,description,sku FROM products WHERE status='published' AND deleted_at IS NULL`)
	return err
}

type variantSpec struct {
	sku, title, opts string
	price            int
}

func variantsFor(p seedProduct) []variantSpec {
	switch p.Cat {
	case "phones", "laptops":
		return []variantSpec{
			{p.SKU + "-BLK", "مشکی / Black", `{"color":"مشکی","storage":"256GB"}`, p.Price},
			{p.SKU + "-SLV", "نقره‌ای / Silver", `{"color":"نقره‌ای","storage":"512GB"}`, p.Price + p.Price/10},
		}
	case "audio", "wearables":
		return []variantSpec{
			{p.SKU + "-BLK", "مشکی / Black", `{"color":"مشکی"}`, p.Price},
			{p.SKU + "-WHT", "سفید / White", `{"color":"سفید"}`, p.Price},
		}
	default:
		return []variantSpec{{p.SKU + "-STD", "استاندارد / Standard", `{}`, p.Price}}
	}
}

func placeholderImage(slug string) string { return "/static/img/products/" + slug + ".svg" }

func seedInventory(ctx context.Context, d *DB, _ *config.Config, rnd *rand.Rand) error {
	variants, err := d.Maps(ctx, `SELECT id FROM product_variants`)
	if err != nil {
		return err
	}
	warehouses, err := d.Maps(ctx, `SELECT id FROM warehouses`)
	if err != nil {
		return err
	}
	for _, v := range variants {
		for i, wrow := range warehouses {
			qty := rnd.Intn(60) + 4
			if i > 0 {
				qty = rnd.Intn(20)
			}
			if _, err := d.Write(ctx,
				`INSERT INTO inventory(variant_id,warehouse_id,quantity,reserved,reorder_level) VALUES(?,?,?,0,5)
				 ON CONFLICT(variant_id,warehouse_id) DO UPDATE SET quantity=excluded.quantity`,
				v["id"], wrow["id"], qty); err != nil {
				return err
			}
			if _, err := d.Write(ctx,
				`INSERT INTO inventory_history(variant_id,warehouse_id,delta,reason,reference)
				 VALUES(?,?,?,'seed','initial-stock')`, v["id"], wrow["id"], qty); err != nil {
				return err
			}
		}
	}
	return nil
}

func seedCommerceConfig(ctx context.Context, d *DB, _ *config.Config, _ *rand.Rand) error {
	methods := [][]any{
		{"zarinpal", "زرین‌پال", "ZarinPal", "sandbox", 0, 1.0, 1},
		{"stripe", "استرایپ", "Stripe", "sandbox", 0, 2.9, 2},
		{"wallet", "کیف پول", "Wallet", "internal", 0, 0.0, 3},
		{"cod", "پرداخت در محل", "Cash on Delivery", "manual", 50000, 0.0, 4},
	}
	for _, m := range methods {
		if _, err := d.Write(ctx,
			`INSERT INTO payment_methods(code,name,name_en,provider,fee_fixed,fee_percent,sort_order,active)
			 VALUES(?,?,?,?,?,?,?,1)
			 ON CONFLICT(code) DO UPDATE SET name=excluded.name, provider=excluded.provider`, m...); err != nil {
			return err
		}
	}

	zones := [][]any{
		{"ایران", "Iran", "IR", ""},
		{"حوزه خلیج فارس", "Gulf States", "AE,QA,OM,KW", ""},
		{"بین‌الملل", "International", "*", ""},
	}
	for _, z := range zones {
		if _, err := d.Write(ctx, `INSERT INTO shipping_zones(name,name_en,countries,provinces,active)
			SELECT ?,?,?,?,1 WHERE NOT EXISTS (SELECT 1 FROM shipping_zones WHERE name=?)`,
			z[0], z[1], z[2], z[3], z[0]); err != nil {
			return err
		}
	}
	var zoneIR, zoneIntl int64
	_ = d.Row(ctx, `SELECT id FROM shipping_zones WHERE countries='IR'`, nil, &zoneIR)
	_ = d.Row(ctx, `SELECT id FROM shipping_zones WHERE countries='*'`, nil, &zoneIntl)

	ship := [][]any{
		{zoneIR, "post-normal", "پست پیشتاز", "Priority Post", "post", 350000, 50000, 5000000, 3, 1},
		{zoneIR, "tipax", "تیپاکس", "Tipax Courier", "tipax", 550000, 80000, 8000000, 2, 2},
		{zoneIR, "express-tehran", "پیک تهران", "Tehran Express", "bike", 250000, 0, 3000000, 1, 3},
		{zoneIntl, "dhl", "دی‌اچ‌ال", "DHL Express", "dhl", 9500000, 1500000, 0, 7, 4},
	}
	for _, s := range ship {
		if _, err := d.Write(ctx,
			`INSERT INTO shipping_methods(zone_id,code,name,name_en,carrier,base_cost,per_kg_cost,free_over,eta_days,sort_order,active)
			 VALUES(?,?,?,?,?,?,?,?,?,?,1)
			 ON CONFLICT(code) DO UPDATE SET base_cost=excluded.base_cost`, s...); err != nil {
			return err
		}
	}

	taxes := [][]any{
		{"مالیات بر ارزش افزوده ایران", "IR", "", "standard", 9.0, 0},
		{"نرخ کاهش‌یافته", "IR", "", "reduced", 5.0, 1},
		{"معاف از مالیات", "IR", "", "zero", 0.0, 2},
		{"EU VAT", "DE", "", "standard", 19.0, 3},
	}
	for _, t := range taxes {
		if _, err := d.Write(ctx, `INSERT INTO tax_rules(name,country,province,tax_class,rate,priority,active)
			SELECT ?,?,?,?,?,?,1 WHERE NOT EXISTS (SELECT 1 FROM tax_rules WHERE name=?)`,
			t[0], t[1], t[2], t[3], t[4], t[5], t[0]); err != nil {
			return err
		}
	}

	coupons := [][]any{
		{"WELCOME10", "percent", 10, 0, 20000000, 1000, 1, "+90 days"},
		{"SAVE50K", "fixed", 500000, 3000000, 0, 500, 2, "+60 days"},
		{"FREESHIP", "free_shipping", 0, 1000000, 0, 0, 3, "+180 days"},
		{"VIP20", "percent", 20, 10000000, 50000000, 100, 1, "+30 days"},
		{"AUTUMN15", "percent", 15, 5000000, 30000000, 300, 2, "+45 days"},
		{"EXPIRED5", "percent", 5, 0, 0, 50, 1, "-5 days"},
	}
	for _, cp := range coupons {
		if _, err := d.Write(ctx, `
			INSERT INTO coupons(code,type,value,min_subtotal,max_discount,usage_limit,per_user_limit,
				starts_at,expires_at,active)
			VALUES(?,?,?,?,?,?,?,datetime('now','-10 days'),datetime('now',?),1)
			ON CONFLICT(code) DO UPDATE SET value=excluded.value, expires_at=excluded.expires_at`,
			cp[0], cp[1], cp[2], cp[3], cp[4], cp[5], cp[6], cp[7]); err != nil {
			return err
		}
	}

	var flashIDs []int
	rows, _ := d.Maps(ctx, `SELECT id FROM products WHERE featured=1 LIMIT 5`)
	for _, r := range rows {
		flashIDs = append(flashIDs, asInt64(r["id"]))
	}
	idList := strings.Trim(strings.Join(strings.Fields(fmt.Sprint(flashIDs)), ","), "[]")
	if _, err := d.Write(ctx, `INSERT INTO campaigns(name,name_en,type,discount_percent,product_ids,starts_at,ends_at,active)
		SELECT ?,?,?,?,?,datetime('now','-1 day'),datetime('now','+14 days'),1
		WHERE NOT EXISTS (SELECT 1 FROM campaigns WHERE name=?)`,
		"جشنواره پاییزی", "Autumn Flash Sale", "flash_sale", 15, "["+idList+"]", "جشنواره پاییزی"); err != nil {
		return err
	}
	return nil
}

func asInt64(v any) int {
	switch t := v.(type) {
	case int64:
		return int(t)
	case int:
		return t
	case float64:
		return int(t)
	}
	return 0
}

func seedVendors(ctx context.Context, d *DB, _ *config.Config, _ *rand.Rand) error {
	type v struct {
		Email, Slug, Fa, En string
		Rate                float64
	}
	vendors := []v{
		{"vendor1@yourstore.com", "digital-world", "دنیای دیجیتال", "Digital World", 12.0},
		{"vendor2@yourstore.com", "smart-home-hub", "خانه هوشمند", "Smart Home Hub", 9.5},
	}
	for _, vd := range vendors {
		var uid int64
		if err := d.Row(ctx, `SELECT id FROM users WHERE email=?`, []any{vd.Email}, &uid); err != nil {
			continue
		}
		res, err := d.Write(ctx, `
			INSERT INTO vendors(user_id,slug,shop_name,shop_name_en,description,commission_rate,balance,status,rating)
			VALUES(?,?,?,?,?,?,?, 'approved', 4.6)
			ON CONFLICT(slug) DO UPDATE SET status='approved'`,
			uid, vd.Slug, vd.Fa, vd.En, "فروشنده رسمی با گارانتی معتبر / Official reseller", vd.Rate, 18500000)
		if err != nil {
			return err
		}
		vid, _ := res.LastInsertId()
		if vid == 0 {
			_ = d.Row(ctx, `SELECT id FROM vendors WHERE slug=?`, []any{vd.Slug}, &vid)
		}
		_, _ = d.Write(ctx, `UPDATE products SET vendor_id=? WHERE vendor_id IS NULL AND id % 2 = ?`, vid, vid%2)
		_, _ = d.Write(ctx, `INSERT INTO vendor_payouts(vendor_id,amount,status,iban,note)
			SELECT ?,?, 'requested','IR000000000000000000000000','payout demo'
			WHERE NOT EXISTS (SELECT 1 FROM vendor_payouts WHERE vendor_id=?)`, vid, 6500000, vid)
	}
	return nil
}

func seedBlog(ctx context.Context, d *DB, _ *config.Config, rnd *rand.Rand) error {
	cats := [][]any{
		{"guides", "راهنمای خرید", "Buying Guides"},
		{"news", "اخبار فناوری", "Tech News"},
		{"reviews", "نقد و بررسی", "Reviews"},
	}
	for _, c := range cats {
		if _, err := d.Write(ctx,
			`INSERT INTO blog_categories(slug,name,name_en) VALUES(?,?,?)
			 ON CONFLICT(slug) DO UPDATE SET name=excluded.name`, c...); err != nil {
			return err
		}
	}
	var authorID int64
	_ = d.Row(ctx, `SELECT id FROM users WHERE role IN ('editor','admin','super_admin') LIMIT 1`, nil, &authorID)

	posts := []struct{ Slug, Fa, En, Cat, Tags string }{
		{"how-to-choose-a-laptop", "چطور لپ‌تاپ مناسب انتخاب کنیم؟", "How to choose the right laptop", "guides", "لپ‌تاپ,راهنما,laptop"},
		{"best-headphones-2026", "بهترین هدفون‌های ۲۰۲۶", "The best headphones of 2026", "reviews", "هدفون,صوتی,audio"},
		{"usb-c-everything", "همه‌چیز درباره USB-C", "Everything about USB-C", "guides", "usb-c,شارژر"},
		{"smart-home-starter", "شروع خانه هوشمند با ۳ دستگاه", "Start your smart home with 3 devices", "guides", "خانه هوشمند,smart home"},
		{"phone-battery-tips", "۷ ترفند افزایش عمر باتری موبایل", "7 tips to extend phone battery life", "news", "موبایل,باتری"},
		{"gaming-laptop-vs-console", "لپ‌تاپ گیمینگ یا کنسول؟", "Gaming laptop or console?", "reviews", "گیمینگ,gaming"},
		{"warranty-explained", "گارانتی اصل چیست؟", "What genuine warranty really means", "guides", "گارانتی,warranty"},
		{"black-friday-guide", "راهنمای خرید در جشنواره‌ها", "A smart shopper's guide to sale season", "news", "تخفیف,sale"},
	}
	for i, p := range posts {
		var catID int64
		_ = d.Row(ctx, `SELECT id FROM blog_categories WHERE slug=?`, []any{p.Cat}, &catID)
		bodyFa := "<p>" + p.Fa + " — در این مقاله گام‌به‌گام بررسی می‌کنیم که هنگام خرید به چه نکاتی توجه کنید، " +
			"چه مشخصاتی واقعاً اهمیت دارند و چطور بودجه خود را بهینه خرج کنید.</p>"
		bodyEn := "<p>" + p.En + " — this article walks through the specifications that actually matter, " +
			"the trade-offs worth making, and how to spend your budget wisely.</p>"
		if _, err := d.Write(ctx, `
			INSERT INTO blog_posts(slug,title,title_en,excerpt,excerpt_en,body,body_en,category_id,author_id,
				tags,status,views,published_at)
			VALUES(?,?,?,?,?,?,?,?,?,?, 'published',?,datetime('now',?))
			ON CONFLICT(slug) DO UPDATE SET title=excluded.title`,
			p.Slug, p.Fa, p.En, strings.TrimSuffix(p.Fa, "؟"), p.En, bodyFa, bodyEn, catID, authorID,
			p.Tags, rnd.Intn(2500), fmt.Sprintf("-%d days", (i+1)*4)); err != nil {
			return err
		}
	}
	return nil
}

func seedOrders(ctx context.Context, d *DB, _ *config.Config, rnd *rand.Rand) error {
	if n, _ := d.Count(ctx, `SELECT COUNT(*) FROM orders`); n > 0 {
		return nil
	}
	customers, err := d.Maps(ctx, `SELECT id,email,first_name,last_name,phone FROM users WHERE role='customer'`)
	if err != nil || len(customers) == 0 {
		return err
	}
	products, err := d.Maps(ctx,
		`SELECT p.id,p.name,p.sku,p.price,p.cost_price,p.vendor_id,
		        (SELECT v.id FROM product_variants v WHERE v.product_id=p.id LIMIT 1) AS variant_id
		 FROM products p`)
	if err != nil {
		return err
	}
	statuses := []string{"delivered", "delivered", "shipped", "processing", "paid", "pending", "cancelled", "refunded"}
	for i := 0; i < 40; i++ {
		cust := customers[rnd.Intn(len(customers))]
		status := statuses[rnd.Intn(len(statuses))]
		payStatus := "paid"
		switch status {
		case "pending":
			payStatus = "unpaid"
		case "cancelled":
			payStatus = "failed"
		case "refunded":
			payStatus = "refunded"
		}
		daysAgo := rnd.Intn(120)
		number := fmt.Sprintf("ORD-%s-%04d", time.Now().UTC().AddDate(0, 0, -daysAgo).Format("20060102"), 1000+i)

		itemCount := rnd.Intn(3) + 1
		subtotal := 0
		chosen := make([]map[string]any, 0, itemCount)
		for j := 0; j < itemCount; j++ {
			chosen = append(chosen, products[rnd.Intn(len(products))])
		}
		coupon := ""
		if rnd.Intn(3) == 0 {
			coupon = []string{"WELCOME10", "SAVE50K", "AUTUMN15"}[rnd.Intn(3)]
		}

		res, err := d.Write(ctx, `
			INSERT INTO orders(number,user_id,email,status,payment_status,fulfillment_status,currency,
				subtotal,discount_total,tax_total,shipping_total,grand_total,coupon_code,payment_method,
				address_json,tracking_code,placed_at,paid_at,created_at,updated_at)
			VALUES(?,?,?,?,?,?, 'IRR',0,0,0,0,0,?,?,?,?,
				datetime('now',?),datetime('now',?),datetime('now',?),datetime('now',?))`,
			number, cust["id"], cust["email"], status, payStatus, fulfillmentFor(status),
			coupon, []string{"zarinpal", "stripe", "wallet", "cod"}[rnd.Intn(4)],
			fmt.Sprintf(`{"recipient":"%s %s","phone":"%s","country":"IR","province":"تهران","city":"تهران","line1":"خیابان ولیعصر، پلاک ۱۲"}`,
				cust["first_name"], cust["last_name"], cust["phone"]),
			strings.ToUpper(auth.RandomToken(5)),
			fmt.Sprintf("-%d days", daysAgo), fmt.Sprintf("-%d days", daysAgo),
			fmt.Sprintf("-%d days", daysAgo), fmt.Sprintf("-%d days", daysAgo))
		if err != nil {
			return err
		}
		orderID, _ := res.LastInsertId()

		for _, p := range chosen {
			qty := rnd.Intn(2) + 1
			unit := asInt64(p["price"])
			total := unit * qty
			subtotal += total
			if _, err := d.Write(ctx, `
				INSERT INTO order_items(order_id,product_id,variant_id,vendor_id,name,sku,quantity,unit_price,cost_price,total,created_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,datetime('now',?))`,
				orderID, p["id"], p["variant_id"], p["vendor_id"], p["name"], p["sku"], qty, unit,
				asInt64(p["cost_price"]), total, fmt.Sprintf("-%d days", daysAgo)); err != nil {
				return err
			}
		}
		discount := 0
		if coupon != "" {
			discount = subtotal / 10
		}
		taxTotal := (subtotal - discount) * 9 / 100
		shipping := 350000
		if subtotal > 5000000 {
			shipping = 0
		}
		grand := subtotal - discount + taxTotal + shipping
		if _, err := d.Write(ctx,
			`UPDATE orders SET subtotal=?,discount_total=?,tax_total=?,shipping_total=?,grand_total=? WHERE id=?`,
			subtotal, discount, taxTotal, shipping, grand, orderID); err != nil {
			return err
		}
		_, _ = d.Write(ctx, `INSERT INTO order_events(order_id,status,note,created_at)
			VALUES(?, 'pending','Order placed',datetime('now',?))`, orderID, fmt.Sprintf("-%d days", daysAgo))
		if payStatus == "paid" || payStatus == "refunded" {
			_, _ = d.Write(ctx, `INSERT INTO order_events(order_id,status,note,created_at)
				VALUES(?, 'paid','Payment captured',datetime('now',?))`, orderID, fmt.Sprintf("-%d days", daysAgo))
			refunded := 0
			payState := "succeeded"
			if payStatus == "refunded" {
				refunded, payState = grand, "refunded"
			}
			_, _ = d.Write(ctx, `
				INSERT INTO payments(reference,order_id,user_id,gateway,amount,currency,status,authority,card_mask,refunded,created_at)
				VALUES(?,?,?,?,?, 'IRR',?,?, '6037-****-****-1234',?,datetime('now',?))`,
				"PAY-"+strings.ToUpper(auth.RandomToken(4)), orderID, cust["id"],
				[]string{"zarinpal", "stripe", "wallet"}[rnd.Intn(3)], grand, payState,
				"A"+auth.RandomToken(8), refunded, fmt.Sprintf("-%d days", daysAgo))
		}
		if status == "shipped" || status == "delivered" {
			deliveredAt := "NULL"
			if status == "delivered" {
				deliveredAt = "datetime('now')"
			}
			_, _ = d.Write(ctx, fmt.Sprintf(`
				INSERT INTO shipments(order_id,carrier,tracking_code,status,shipped_at,delivered_at)
				VALUES(?,?,?,?,datetime('now',?),%s)`, deliveredAt),
				orderID, "post", "TRK"+strings.ToUpper(auth.RandomToken(6)),
				map[string]string{"shipped": "in_transit", "delivered": "delivered"}[status],
				fmt.Sprintf("-%d days", daysAgo))
		}
		if coupon != "" {
			var cid int64
			if err := d.Row(ctx, `SELECT id FROM coupons WHERE code=?`, []any{coupon}, &cid); err == nil {
				_, _ = d.Write(ctx, `UPDATE coupons SET used_count = used_count + 1 WHERE id=?`, cid)
				_, _ = d.Write(ctx,
					`INSERT INTO coupon_redemptions(coupon_id,user_id,order_id,amount,created_at)
					 VALUES(?,?,?,?,datetime('now',?))`,
					cid, cust["id"], orderID, discount, fmt.Sprintf("-%d days", daysAgo))
			}
		}
		if payStatus == "paid" {
			_, _ = d.Write(ctx,
				`UPDATE loyalty_accounts SET points=points+?, lifetime=lifetime+? WHERE user_id=?`,
				grand/10000, grand/10000, cust["id"])
		}
	}

	for i := 0; i < 6; i++ {
		cust := customers[rnd.Intn(len(customers))]
		res, err := d.Write(ctx,
			`INSERT INTO carts(token,user_id,currency,status,created_at,updated_at)
			 VALUES(?,?, 'IRR','active',datetime('now',?),datetime('now',?))`,
			auth.RandomToken(16), cust["id"],
			fmt.Sprintf("-%d hours", 8+i*3), fmt.Sprintf("-%d hours", 8+i*3))
		if err != nil {
			return err
		}
		cartID, _ := res.LastInsertId()
		p := products[rnd.Intn(len(products))]
		_, _ = d.Write(ctx,
			`INSERT INTO cart_items(cart_id,product_id,variant_id,quantity,unit_price) VALUES(?,?,?,?,?)`,
			cartID, p["id"], p["variant_id"], rnd.Intn(2)+1, asInt64(p["price"]))
	}
	return nil
}

func fulfillmentFor(status string) string {
	switch status {
	case "shipped":
		return "shipped"
	case "delivered":
		return "fulfilled"
	default:
		return "unfulfilled"
	}
}

func seedReviews(ctx context.Context, d *DB, _ *config.Config, rnd *rand.Rand) error {
	if n, _ := d.Count(ctx, `SELECT COUNT(*) FROM reviews`); n > 0 {
		return nil
	}
	products, _ := d.Maps(ctx, `SELECT id FROM products`)
	customers, _ := d.Maps(ctx, `SELECT id FROM users WHERE role='customer'`)
	if len(products) == 0 || len(customers) == 0 {
		return nil
	}
	titlesFa := []string{"عالی بود", "ارزش خرید دارد", "کیفیت ساخت بالا", "نسبت به قیمت خوب است", "کمی گران"}
	titlesEn := []string{"Excellent", "Worth the money", "Great build quality", "Good for the price", "A bit pricey"}
	bodies := []string{
		"بسته‌بندی مرتب بود و کالا دقیقاً مطابق توضیحات ارسال شد.",
		"بعد از دو هفته استفاده کاملاً راضی هستم؛ باتری خوبی دارد.",
		"Packaging was neat and the item matched the description exactly.",
		"Two weeks in and I am very happy with it — battery life is solid.",
		"ارسال سریع بود ولی جعبه کمی ضربه دیده بود.",
	}
	for _, p := range products {
		for i := 0; i < rnd.Intn(5)+1; i++ {
			cust := customers[rnd.Intn(len(customers))]
			rating := []int{5, 5, 4, 4, 3, 5, 2}[rnd.Intn(7)]
			status := "approved"
			if rnd.Intn(8) == 0 {
				status = "pending"
			}
			idx := rnd.Intn(len(titlesFa))
			title := titlesFa[idx]
			if rnd.Intn(2) == 0 {
				title = titlesEn[idx]
			}
			if _, err := d.Write(ctx, `
				INSERT INTO reviews(product_id,user_id,rating,title,body,verified,status,helpful,created_at)
				VALUES(?,?,?,?,?,1,?,?,datetime('now',?))`,
				p["id"], cust["id"], rating, title, bodies[rnd.Intn(len(bodies))], status,
				rnd.Intn(20), fmt.Sprintf("-%d days", rnd.Intn(90))); err != nil {
				return err
			}
		}
	}
	_, err := d.Write(ctx, `
		UPDATE products SET
			rating_avg = COALESCE((SELECT ROUND(AVG(rating),2) FROM reviews r
				WHERE r.product_id=products.id AND r.status='approved'),0),
			rating_count = COALESCE((SELECT COUNT(*) FROM reviews r
				WHERE r.product_id=products.id AND r.status='approved'),0)`)
	return err
}

func seedEngagement(ctx context.Context, d *DB, _ *config.Config, rnd *rand.Rand) error {
	plans := [][]any{
		{"basic", "پایه", "Basic", "ارسال رایگان نامحدود", 290000, "monthly", 7, `["ارسال رایگان","پشتیبانی ایمیلی"]`},
		{"plus", "پلاس", "Plus", "ارسال رایگان + ۵٪ تخفیف همیشگی", 590000, "monthly", 7, `["ارسال رایگان","۵٪ تخفیف","پشتیبانی اولویت‌دار"]`},
		{"pro-annual", "حرفه‌ای سالانه", "Pro Annual", "همه مزایا با پرداخت سالانه", 5900000, "yearly", 14, `["ارسال رایگان","۱۰٪ تخفیف","پشتیبانی ۲۴ ساعته"]`},
	}
	for _, p := range plans {
		if _, err := d.Write(ctx,
			`INSERT INTO subscription_plans(code,name,name_en,description,price,interval,trial_days,features_json,active)
			 VALUES(?,?,?,?,?,?,?,?,1)
			 ON CONFLICT(code) DO UPDATE SET price=excluded.price`, p...); err != nil {
			return err
		}
	}
	customers, _ := d.Maps(ctx, `SELECT id FROM users WHERE role='customer' LIMIT 3`)
	for i, cust := range customers {
		var planID int64
		_ = d.Row(ctx, `SELECT id FROM subscription_plans LIMIT 1 OFFSET ?`, []any{i % 3}, &planID)
		if planID == 0 {
			continue
		}
		res, err := d.Write(ctx, `
			INSERT INTO subscriptions(user_id,plan_id,status,started_at,renews_at)
			SELECT ?,?,?,datetime('now','-40 days'),datetime('now',?)
			WHERE NOT EXISTS (SELECT 1 FROM subscriptions WHERE user_id=? AND plan_id=?)`,
			cust["id"], planID, []string{"active", "active", "past_due"}[i%3],
			fmt.Sprintf("+%d days", 5+i*7), cust["id"], planID)
		if err != nil {
			return err
		}
		if sid, _ := res.LastInsertId(); sid > 0 {
			_, _ = d.Write(ctx, `INSERT INTO subscription_invoices(subscription_id,amount,status,period_start,period_end)
				VALUES(?,?, 'paid',datetime('now','-40 days'),datetime('now','-10 days'))`, sid, 290000)
		}
	}

	for i := 0; i < 5; i++ {
		var owner int64
		_ = d.Row(ctx, `SELECT id FROM users WHERE role='customer' LIMIT 1 OFFSET ?`, []any{i % 3}, &owner)
		amount := (i + 1) * 500000
		if _, err := d.Write(ctx, `
			INSERT INTO gift_cards(code,initial_amount,balance,currency,owner_id,expires_at,status)
			VALUES(?,?,?, 'IRR',?,datetime('now','+1 year'),'active')
			ON CONFLICT(code) DO NOTHING`,
			fmt.Sprintf("GC-DEMO%04d", 1000+i), amount, amount, owner); err != nil {
			return err
		}
	}

	flags := [][]any{
		{"new_checkout", "صفحه پرداخت تک‌مرحله‌ای / One-page checkout", 1, 50, ""},
		{"wishlist_sharing", "اشتراک‌گذاری لیست علاقه‌مندی / Share wishlists", 1, 100, ""},
		{"ai_recommendations", "پیشنهاد هوشمند محصول / AI recommendations", 0, 0, ""},
		{"vendor_self_payout", "تسویه خودکار فروشنده / Vendor self-payout", 1, 100, "admin,super_admin"},
		{"dark_mode", "حالت تاریک / Dark mode", 1, 100, ""},
	}
	for _, f := range flags {
		if _, err := d.Write(ctx,
			`INSERT INTO feature_flags(key,description,enabled,rollout,roles) VALUES(?,?,?,?,?)
			 ON CONFLICT(key) DO UPDATE SET description=excluded.description`, f...); err != nil {
			return err
		}
	}

	tests := [][]any{
		{"hero_cta", "متن دکمه هیرو / Hero CTA copy", "A,B"},
		{"price_display", "نمایش قیمت / Price display", "with_tax,without_tax"},
	}
	for _, t := range tests {
		res, err := d.Write(ctx,
			`INSERT INTO ab_tests(key,name,variants,status) VALUES(?,?,?, 'running')
			 ON CONFLICT(key) DO UPDATE SET name=excluded.name`, t...)
		if err != nil {
			return err
		}
		tid, _ := res.LastInsertId()
		if tid == 0 {
			_ = d.Row(ctx, `SELECT id FROM ab_tests WHERE key=?`, []any{t[0]}, &tid)
		}
		for _, variant := range strings.Split(t[2].(string), ",") {
			imp := rnd.Intn(800) + 200
			_, _ = d.Write(ctx,
				`INSERT INTO ab_results(test_id,variant,impressions,conversions) VALUES(?,?,?,?)
				 ON CONFLICT(test_id,variant) DO NOTHING`, tid, variant, imp, rnd.Intn(imp/4+1))
		}
	}

	queries := []string{"لپ تاپ", "هدفون", "iphone", "powerbank", "ساعت هوشمند", "گیمینگ", "usb-c", "قهوه ساز"}
	for i := 0; i < 60; i++ {
		q := queries[rnd.Intn(len(queries))]
		_, _ = d.Write(ctx, `INSERT INTO search_log(query,results,created_at) VALUES(?,?,datetime('now',?))`,
			q, rnd.Intn(24), fmt.Sprintf("-%d hours", rnd.Intn(480)))
	}
	events := []string{"page_view", "product_view", "add_to_cart", "begin_checkout", "purchase", "search"}
	sources := []string{"direct", "google", "instagram", "telegram", "email"}
	for i := 0; i < 400; i++ {
		_, _ = d.Write(ctx,
			`INSERT INTO analytics_events(name,source,value,created_at) VALUES(?,?,?,datetime('now',?))`,
			events[rnd.Intn(len(events))], sources[rnd.Intn(len(sources))], rnd.Intn(1000),
			fmt.Sprintf("-%d hours", rnd.Intn(720)))
	}
	return nil
}

func seedOps(ctx context.Context, d *DB, cfg *config.Config, _ *rand.Rand) error {
	hooks := [][]any{
		{"Order pipeline", "https://webhook.site/arena-demo", "order.created,order.paid,order.shipped", auth.RandomToken(16)},
		{"Inventory alerts", "https://example.com/hooks/inventory", "inventory.low_stock", auth.RandomToken(16)},
	}
	for _, h := range hooks {
		if _, err := d.Write(ctx, `INSERT INTO webhooks(name,url,events,secret,active)
			SELECT ?,?,?,?,1 WHERE NOT EXISTS (SELECT 1 FROM webhooks WHERE url=?)`,
			h[0], h[1], h[2], h[3], h[1]); err != nil {
			return err
		}
	}
	reports := [][]any{
		{"sales", "0 6 * * 1", "csv", "admin@yourstore.com"},
		{"inventory-valuation", "0 7 1 * *", "csv", "admin@yourstore.com"},
		{"vendor-commissions", "0 8 * * 1", "csv", "admin@yourstore.com"},
	}
	for _, r := range reports {
		if _, err := d.Write(ctx, `INSERT INTO scheduled_reports(report_key,cron,format,recipients,active)
			SELECT ?,?,?,?,1 WHERE NOT EXISTS (SELECT 1 FROM scheduled_reports WHERE report_key=?)`,
			r[0], r[1], r[2], r[3], r[0]); err != nil {
			return err
		}
	}
	seo := [][]any{
		{"page", "about", "درباره فروشگاه آرنا | About Arena Store",
			"فروشگاه اینترنتی کالای دیجیتال با گارانتی معتبر و ارسال سریع."},
		{"product", "1", "خرید مک‌بوک ایر M3 | Buy MacBook Air M3",
			"مک‌بوک ایر M3 با گارانتی ۱۸ ماهه و ارسال رایگان."},
	}
	for _, s := range seo {
		if _, err := d.Write(ctx,
			`INSERT INTO seo_meta(entity_type,entity_id,title,description) VALUES(?,?,?,?)
			 ON CONFLICT(entity_type,entity_id) DO UPDATE SET title=excluded.title`, s...); err != nil {
			return err
		}
	}
	_, _ = d.Write(ctx, `INSERT INTO redirects(from_path,to_path,code)
		SELECT '/old-shop','/shop',301 WHERE NOT EXISTS (SELECT 1 FROM redirects WHERE from_path='/old-shop')`)

	_, _ = d.Write(ctx, `INSERT INTO api_keys(name,key_hash,prefix,scopes)
		SELECT 'Demo integration',?,?, 'read:products,read:orders'
		WHERE NOT EXISTS (SELECT 1 FROM api_keys WHERE name='Demo integration')`,
		auth.HashToken("demo-key-please-rotate"), "ak_demo")

	_, _ = d.Write(ctx, `INSERT INTO queue_jobs(queue,name,payload_json,status,attempts,max_attempts,result,duration_ms,finished_at)
		SELECT 'webhook','webhook.deliver','{"event":"order.created"}','completed',1,3,'dispatched',42,datetime('now','-1 hour')
		WHERE NOT EXISTS (SELECT 1 FROM queue_jobs)`)
	_, _ = d.Write(ctx, `INSERT INTO error_log(level,code,message,path)
		SELECT 'warn','SEED','Demo data generated for `+cfg.AppName+`','/seed'
		WHERE NOT EXISTS (SELECT 1 FROM error_log)`)
	return nil
}

var cronRegistry = []struct{ Name, Spec, Retry, Description string }{
	{"abandoned-cart-reminder", "0 * * * *", "3x / backoff", "Notify customers whose cart has been idle for more than 24 hours"},
	{"order-auto-cancel-unpaid", "*/15 * * * *", "2x", "Cancel pending orders that were never paid within the payment window"},
	{"inventory-low-stock-alert", "0 8 * * *", "1x", "Alert administrators about variants at or below their reorder level"},
	{"currency-exchange-rate-sync", "0 */6 * * *", "5x / backoff", "Refresh FX rates for every enabled currency"},
	{"flash-sale-price-activator", "* * * * *", "3x", "Activate and expire flash-sale campaigns on schedule"},
	{"subscription-renewal-billing", "0 2 * * *", "5x", "Charge due subscriptions and issue recurring invoices"},
	{"license-expiry-check", "0 3 * * *", "2x", "Verify platform licence validity and warn before expiry"},
	{"coupon-expiry-cleanup", "0 0 * * *", "1x", "Deactivate coupons past their expiry date or usage limit"},
	{"vendor-payout-processing", "0 4 * * 1", "3x", "Approve weekly vendor payout requests and credit wallets"},
	{"tax-report-generation", "0 5 1 * *", "2x", "Generate the monthly tax summary and store it as an analytics event"},
	{"database-backup", "0 1 * * *", "3x + Alert", "Nightly encrypted-at-rest SQLite snapshot with rotation"},
	{"cache-warmup", "*/30 * * * *", "1x", "Pre-compute the hot product and dashboard caches"},
	{"sitemap-regeneration", "0 0 * * *", "2x", "Rebuild sitemap.xml from published catalog and content"},
	{"search-index-rebuild", "0 0 * * *", "2x", "Rebuild the FTS5 product search index"},
	{"analytics-aggregation", "0 * * * *", "2x", "Roll hourly analytics into aggregate counters"},
	{"review-reminder-email", "0 9 * * *", "1x", "Ask customers to review products delivered a week ago"},
	{"session-cleanup", "0 0 * * *", "1x", "Purge expired refresh tokens, auth tokens and idempotency keys"},
	{"fraud-detection-scan", "0 * * * *", "3x", "Flag suspicious payment velocity and failed-login bursts"},
	{"gift-card-expiry-check", "0 6 * * *", "1x", "Expire gift cards past their validity date"},
	{"loyalty-points-expiry", "0 0 1 * *", "1x", "Expire loyalty points older than the retention window"},
	{"webhook-retry-failed", "*/5 * * * *", "5x / backoff", "Re-deliver failed webhook payloads with exponential backoff"},
	{"invoice-pdf-retry", "*/10 * * * *", "3x", "Regenerate invoice documents for orders missing one"},
	{"log-rotation-cleanup", "0 0 * * 0", "1x", "Rotate audit, error and analytics logs beyond the retention window"},
	{"health-check-ping", "*/5 * * * *", "-", "Probe database, queue and disk health and record the result"},
}

func seedCronJobs(ctx context.Context, d *DB, _ *config.Config, _ *rand.Rand) error {
	for _, j := range cronRegistry {
		if _, err := d.Write(ctx,
			`INSERT INTO cron_jobs(name,schedule,description,retry_policy)
			 VALUES(?,?,?,?)
			 ON CONFLICT(name) DO UPDATE SET schedule=excluded.schedule,
			   description=excluded.description, retry_policy=excluded.retry_policy,
			   updated_at=datetime('now')`,
			j.Name, j.Spec, j.Description, j.Retry); err != nil {
			return err
		}
	}
	return nil
}

func CronRegistryNames() []string {
	out := make([]string, 0, len(cronRegistry))
	for _, j := range cronRegistry {
		out = append(out, j.Name)
	}
	return out
}
