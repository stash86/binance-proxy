# Candle cache freshness and recovery

The spot and futures kline endpoints use the WebSocket cache only after REST
history has loaded and a newer, valid Binance kline event has arrived. Otherwise
they use the existing rate-limited REST forwarding path.

A cached series becomes unavailable when:

- No accepted WebSocket update has arrived for more than 15 seconds, or the
  Binance event timestamp is more than 15 seconds old.
- The last candle has ended without a confirmed final update.
- The last candle ended more than 5 seconds ago, even if it was finalized.
- The connection reports an error, is being replaced, or the service stops.

The 5-second boundary grace allows a finalized previous candle to be served
while the next candle's first update is in flight. An unfinished previous candle
does not receive this grace. The existing fake-candle option applies only to
eligible cache responses; it cannot make an unhealthy cache eligible. Use
`--disable-fake-candles` when only exchange-provided candles are desired.

The connection watchdog checks once a second and replaces connections after
30 seconds without accepted fresh data. New connections and history repair have
the same 30-second allowance. Recovery cancels the old connection's REST work,
clears the cache, stops the socket, and waits for its callback to finish before
opening a replacement. A replacement reloads up to 1,000 candles through REST.
Missing candles and missed final updates also trigger history repair.

Retry delays grow through 2, 4, 8, 16, 32, and 60 seconds, with each wait randomly
chosen between half and all of that delay. The sequence resets only after a
minute of continuously healthy delivery, rather than after a successful
handshake. History-download retries use the same delay policy.

Initial candle connections and reconnects share an allowance of one attempt
every 1.5 seconds per market type, per process, with an initial burst of one.
This spreads startup and recovery work; bringing up hundreds of distinct series
can take several minutes, during which requests may use REST. This limiter does
not account for depth/ticker connections, other processes, or other clients
sharing the public IP.

These thresholds apply to feed updates, including unchanged OHLCV values, rather
than the candle timeframe. A daily candle can therefore become unavailable
while its closing time is still hours away. Old, duplicate, or buffered events
cannot renew freshness. Clock comparisons use Binance event timestamps and the
host clock; hosts should keep their clocks synchronized.

REST forwarding can still fail or be rate-limited. Recovery does not substitute
old cache data for such failures. The existing `/status` response has no
per-series freshness fields, and time-bounded requests still bypass the cache.
This change does not introduce clustering or combined WebSocket subscriptions.
