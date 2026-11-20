// openEvents opens BluForge's event stream and keeps it honest.
//
// The stream is a live hint, not a record. It has no replay, so anything that
// happens while it is down is lost unless the page asks the server. Three
// things take it down:
//   - the server closes a client that falls behind (it does not skip events);
//     the browser reconnects by itself;
//   - the connection dies without closing, as when a machine sleeps or a
//     network drops; nothing tells the browser, so pings detect it;
//   - the server refuses a reconnect; the browser gives up for good.
// Each reconnect calls resync, which replaces the page's state with the
// server's. resync also runs when the tab becomes visible.
//
// The server pings every 15 seconds. Three missed pings means the stream is
// dead, so it is closed and opened again.
//
// Returns an object with on(type, fn), which survives reconnects.
function openEvents(resync) {
	const PING_DEADLINE_MS = 45000
	const listeners = []
	let es = null
	let everOpen = false
	let lastSeen = Date.now()

	function seen() { lastSeen = Date.now() }

	function connect() {
		es = new EventSource('/events')
		es.addEventListener('open', () => {
			seen()
			if (everOpen) resync()
			everOpen = true
		})
		es.addEventListener('ping', seen)
		for (const [type, fn] of listeners) {
			es.addEventListener(type, (e) => { seen(); fn(e) })
		}
	}

	setInterval(() => {
		if (Date.now() - lastSeen < PING_DEADLINE_MS) return
		console.warn('BluForge: event stream silent; reopening it')
		es.close()
		seen()
		// A fresh EventSource's first open would otherwise count as the
		// first-ever open and skip the resync this reopen exists for.
		everOpen = true
		connect()
	}, 5000)

	document.addEventListener('visibilitychange', () => {
		if (document.visibilityState === 'visible') resync()
	})

	connect()

	return {
		on(type, fn) {
			listeners.push([type, fn])
			es.addEventListener(type, (e) => { seen(); fn(e) })
		},
	}
}
