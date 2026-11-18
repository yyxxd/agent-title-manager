package engine

import (
	"context"
	"database/sql"
)

func titleConflict(ctx context.Context, tx *sql.Tx, id, title string) (bool, error) {
	var conflict bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM events target JOIN sessions owner USING(agent,session_id)
		JOIN sessions peer ON peer.project_key=owner.project_key AND peer.project_key<>''
		WHERE target.id=? AND (peer.agent<>owner.agent OR peer.session_id<>owner.session_id)
		AND (peer.title=? OR EXISTS(SELECT 1 FROM events other
			WHERE other.agent=peer.agent AND other.session_id=peer.session_id AND other.revision=peer.revision
			AND other.title=? AND other.status IN ('candidate','claimed')))
	)`, id, title, title).Scan(&conflict)
	return conflict, err
}
