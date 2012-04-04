package fsnotify_test

func ExampleWatcher() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	err = watcher.Watch("/tmp")
	if err != nil {
		log.Fatal(err)
	}
	for {
		select {
		case ev := <-watcher.Event:
			log.Println("event:", ev)
		case err := <-watcher.Error:
			log.Println("error:", err)
		}
	}
}
