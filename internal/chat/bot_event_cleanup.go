package chat

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

func startBotEventCleanup(db *sql.DB) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := pruneExpiredBotEvents(ctx, db, now); err != nil && ctx.Err() == nil {
					slog.Error("清理 Bot 事件失败", "type", fmt.Sprintf("%T", err))
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func pruneExpiredBotEvents(ctx context.Context, db *sql.DB, now time.Time) error {
	cutoff := now.Add(-botEventRetention).UnixMilli()
	return transaction(ctx, db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `WITH expired AS (
			SELECT installation_id,max(sequence) AS sequence FROM bot_events
			WHERE occurred_at<=? GROUP BY installation_id
		)
		UPDATE bot_installations SET pruned_event_sequence=max(pruned_event_sequence,(
			SELECT sequence FROM expired WHERE expired.installation_id=bot_installations.id
		)) WHERE id IN (SELECT installation_id FROM expired)
		RETURNING id,pruned_event_sequence`, cutoff)
		if err != nil {
			return err
		}
		defer rows.Close()
		type boundary struct {
			installationID string
			sequence       int64
		}
		var boundaries []boundary
		for rows.Next() {
			var expired boundary
			if err := rows.Scan(&expired.installationID, &expired.sequence); err != nil {
				return err
			}
			boundaries = append(boundaries, expired)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, boundary := range boundaries {
			if _, err := tx.ExecContext(ctx, `DELETE FROM bot_events WHERE installation_id=? AND sequence<=?`,
				boundary.installationID, boundary.sequence); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM bot_command_requests WHERE accepted_at<=?", cutoff)
		return err
	})
}
