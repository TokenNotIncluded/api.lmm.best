package model

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Does not start an application, payment provider, worker, or notification path.
func TestSubscriptionCheckoutConfirmation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "checkout.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	oldDB := DB
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&SubscriptionOrder{}, &UserSubscription{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	write := func(sql string, args ...interface{}) {
		t.Helper()
		if err := db.Exec(sql, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	read := func(trade string) *SubscriptionCheckoutConfirmation {
		t.Helper()
		result, err := GetSubscriptionCheckoutConfirmation(ctx, 1, trade)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	write("INSERT INTO user_subscriptions (id,user_id,plan_id,amount_total,amount_used,status) VALUES (42,1,3,1000,10,'active'),(43,1,3,1000,0,'active'),(44,2,3,1000,0,'active'),(45,1,4,1000,0,'active')")
	write("INSERT INTO subscription_orders (id,user_id,plan_id,trade_no,status,complete_time,user_subscription_id) VALUES (1,1,3,'order-A','pending',0,0),(2,1,3,'order-B','success',200,43),(3,2,3,'other-user','success',200,44)")
	for _, mutation := range []string{
		"UPDATE user_subscriptions SET amount_used=11 WHERE id=42",
		"UPDATE user_subscriptions SET amount_used=0,next_reset_time=20000 WHERE id=42",
		"UPDATE user_subscriptions SET status='expired',end_time=150 WHERE id=42",
	} {
		write(mutation)
		if read("order-A").Confirmed {
			t.Fatal("old subscription mutation confirmed a pending order")
		}
	}
	if !read("order-B").Confirmed || read("order-A").Confirmed {
		t.Fatal("another purchase must not replace this order's evidence")
	}
	for _, trade := range []string{"other-user", "absent", "order-A' OR 1=1 --"} {
		if _, err := GetSubscriptionCheckoutConfirmation(ctx, 1, trade); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("expected indistinguishable not-found for %q, got %v", trade, err)
		}
	}
	write("UPDATE subscription_orders SET status='success',complete_time=200 WHERE id=1")
	for _, grant := range []int{0, 44, 45, 999} {
		write("UPDATE subscription_orders SET user_subscription_id=? WHERE id=1", grant)
		result := read("order-A")
		if result.Confirmed || result.UserSubscriptionId != 0 {
			t.Fatalf("invalid owner/plan/missing grant accepted: %+v", result)
		}
	}
	write("INSERT INTO user_subscriptions (id,user_id,plan_id,amount_total,amount_used,status) VALUES (46,1,3,1000,0,'active')")
	write("UPDATE subscription_orders SET user_subscription_id=46 WHERE id=1")
	for i := 0; i < 3; i++ {
		result := read("order-A")
		if !result.Confirmed || result.UserSubscriptionId != 46 {
			t.Fatalf("matching paid grant not confirmed: %+v", result)
		}
	}
	var orders, grants int64
	if err := db.Model(&SubscriptionOrder{}).Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&UserSubscription{}).Count(&grants).Error; err != nil {
		t.Fatal(err)
	}
	if orders != 3 || grants != 5 {
		t.Fatalf("read mutated records: orders=%d grants=%d", orders, grants)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := GetSubscriptionCheckoutConfirmation(cancelled, 1, "order-A"); err == nil {
		t.Fatal("cancelled query must not succeed")
	}
	if err := db.Migrator().DropTable(&UserSubscription{}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetSubscriptionCheckoutConfirmation(ctx, 1, "order-A"); err == nil {
		t.Fatal("database read failure must not become successful evidence")
	}
}
