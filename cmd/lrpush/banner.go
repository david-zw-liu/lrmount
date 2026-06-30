package main

func warningBanner() string {
	return "" +
		"========================== IMPORTANT ==========================\n" +
		" Before pushing, fully close Lightroom on the iPhone\n" +
		" (swipe it away in the app switcher). Re-open it AFTER pushing.\n" +
		" Otherwise the app's save flow may overwrite what we write.\n" +
		"\n" +
		" Note: presets pushed this way may appear only on this device\n" +
		" and may NOT sync to Creative Cloud.\n" +
		"===============================================================\n"
}
