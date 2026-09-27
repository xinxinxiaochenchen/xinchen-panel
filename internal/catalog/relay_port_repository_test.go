package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRelayPortReservation(t *testing.T) {
	databaseURL := catalogTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var groupID, nodeID string
	err = tx.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),'TEST.RELAYPORT','Relay ports','JP') RETURNING id::text`).Scan(&groupID)
	if err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities,proxy_port,relay_port)
VALUES(gen_random_uuid(),$1,'Relay','JP','relay-port.example.invalid',ARRAY['proxy','forward'],443,24443) RETURNING id::text`, groupID).Scan(&nodeID)
	if err != nil {
		t.Fatal(err)
	}
	assertReservations := func(port, expected int) {
		t.Helper()
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM port_allocations WHERE node_id=$1 AND port=$2 AND owner_type='node_relay' AND released_at IS NULL`, nodeID, port).Scan(&count); err != nil || count != expected {
			t.Fatalf("relay reservations = %d, expected %d: %v", count, expected, err)
		}
	}
	assertReservations(24443, 2)
	if _, err := tx.Exec(ctx, `SAVEPOINT endpoint_collision`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities,proxy_port)
VALUES(gen_random_uuid(),$1,'Collision','JP','relay-port.example.invalid',ARRAY['proxy'],24443)`, groupID)
	var endpointError *pgconn.PgError
	if !errors.As(err, &endpointError) || endpointError.Code != "23505" {
		t.Fatalf("relay endpoint accepted as proxy on another node: %v", err)
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT endpoint_collision`); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"TCP", "UDP"} {
		if _, err := tx.Exec(ctx, `SAVEPOINT occupied`); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO port_allocations(id,node_id,protocol,port,owner_type,owner_id) VALUES(gen_random_uuid(),$1,$2,24443,'forward_rule',gen_random_uuid())`, nodeID, protocol)
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "23505" {
			t.Fatalf("relay port could be allocated by %s forward: %v", protocol, err)
		}
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT occupied`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET enabled=false WHERE id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	assertReservations(24443, 2)
	if _, err := tx.Exec(ctx, `UPDATE nodes SET relay_port=24444 WHERE id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	assertReservations(24443, 0)
	assertReservations(24444, 2)
	if _, err := tx.Exec(ctx, `INSERT INTO port_allocations(id,node_id,protocol,port,owner_type,owner_id) VALUES(gen_random_uuid(),$1,'TCP',24445,'forward_rule',gen_random_uuid())`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT occupied`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE nodes SET relay_port=24445 WHERE id=$1`, nodeID)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23505" {
		t.Fatalf("relay could claim forward port: %v", err)
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT occupied`); err != nil {
		t.Fatal(err)
	}
	assertReservations(24444, 2)
	if _, err := tx.Exec(ctx, `DELETE FROM port_allocations WHERE node_id=$1 AND owner_type='forward_rule'`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	assertReservations(24444, 0)
}
