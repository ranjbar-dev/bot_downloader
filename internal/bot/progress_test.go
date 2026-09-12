package bot

import "testing"

func TestDeliveryKeyboardOffersAllModes(t *testing.T) {
	keyboard := deliveryKeyboard()
	if len(keyboard.InlineKeyboard) != 3 {
		t.Fatalf("delivery rows = %d, want 3", len(keyboard.InlineKeyboard))
	}
	want := []string{
		deliveryCallbackPrefix + string(deliveryDirect),
		deliveryCallbackPrefix + string(deliveryTelegram),
		deliveryCallbackPrefix + string(deliveryBoth),
	}
	for i, callback := range want {
		if got := keyboard.InlineKeyboard[i][0].CallbackData; got != callback {
			t.Errorf("row %d callback = %q, want %q", i, got, callback)
		}
	}
}

func TestProgressText(t *testing.T) {
	tests := []struct {
		phase   progressPhase
		percent int
		want    string
	}{
		{phaseQueued, 0, "🚦 In queue..."},
		{phaseDownloading, 0, "⬇️ Downloading..."},
		{phaseDownloading, 50, "⬇️ Downloading 50%"},
		{phaseDownloading, 100, "⬇️ Downloading 100%"},
		{phaseProcessing, 100, "⚙️ Processing the downloaded media..."},
		{phaseDirectLink, 100, "🔗 Making the direct download link..."},
		{phaseUploading, 30, "📤 Uploading to Telegram... 30%"},
	}
	for _, tt := range tests {
		if got := progressText(tt.phase, tt.percent); got != tt.want {
			t.Errorf("progressText(%d, %d) = %q, want %q", tt.phase, tt.percent, got, tt.want)
		}
	}
}

func TestDeliveryLabels(t *testing.T) {
	if deliveryLabel(deliveryDirect) != "Direct download" {
		t.Fatal("direct delivery label changed")
	}
	if deliveryLabel(deliveryTelegram) != "Telegram download" {
		t.Fatal("telegram delivery label changed")
	}
	if deliveryLabel(deliveryBoth) != "Telegram + direct download" {
		t.Fatal("both delivery label changed")
	}
}
