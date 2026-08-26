package pq

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"testing/fstest"

	"github.com/lib/pq"
	"zgo.at/zdb"
	"zgo.at/zdb/drivers"
	"zgo.at/zstd/zcrypto"
)

func init() {
	set := func(k, v string) {
		if _, ok := os.LookupEnv(k); !ok {
			os.Setenv(k, v)
		}
	}
	set("PGHOST", "localhost")
	set("PGPORT", "5432")
	set("PGDATABASE", "pq")
	set("PGUSER", "pq")
	set("PGPASSWORD", "unused")
	set("PGSSLMODE", "disable")
}

func TestErrUnqiue(t *testing.T) {
	tests := []struct {
		err   error
		check func(error) bool
		want  bool
	}{
		{&pq.Error{}, driver{}.ErrUnique, false},
		{&pq.Error{Code: "123"}, driver{}.ErrUnique, false},
		{&pq.Error{Code: "23505"}, driver{}.ErrUnique, true},
		{fmt.Errorf("X: %w", &pq.Error{Code: "23505"}), driver{}.ErrUnique, true},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			out := tt.check(tt.err)
			if out != tt.want {
				t.Errorf("out: %t; want: %t", out, tt.want)
			}
		})
	}
}

func TestConnectCreate(t *testing.T) {
	dbname := "pqtest_" + zcrypto.SecretString(10, "")

	d, ctx := driver{}, context.Background()

	{ // Connect with create false → should error out.
		db, err := d.Connect(ctx, "dbname="+dbname, false)
		if _, ok := errors.AsType[*drivers.NotExistError](err); !ok {
			t.Fatalf("wrong error: %v", err)
		}
		if db != nil {
			t.Fatalf("db not null: %v", db)
		}
	}

	{ // connect with create → should create db
		db, err := d.Connect(ctx, "dbname="+dbname, true)
		if err != nil {
			t.Fatal(err)
		}
		err = db.Close()
		if err != nil {
			t.Fatal(err)
		}
		// Clean up our test database.
		db, err = d.Connect(ctx, "dbname=postgres", false)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		_, err = db.Exec(fmt.Sprintf(`drop database "%s"`, dbname))
		if err != nil {
			t.Fatal(err)
		}
	}

}

func TestStartTest(t *testing.T) {
	d := driver{}
	ctx := d.StartTest(t, &drivers.TestOptions{
		Files: fstest.MapFS{
			"db/schema.sql":    &fstest.MapFile{Data: []byte("create table a (i int)")},
			"db/migrate/1.sql": &fstest.MapFile{Data: []byte("create table b (j int)")},
			"db/migrate/2.sql": &fstest.MapFile{Data: []byte("insert into a values (123); insert into b values (456);")},
		},
	})

	var a, b int
	if err := zdb.Get(ctx, &a, `select i from a`); err != nil {
		t.Fatal(err)
	}
	if err := zdb.Get(ctx, &b, `select j from b`); err != nil {
		t.Fatal(err)
	}
	if a != 123 || b != 456 {
		t.Errorf("%v %v", a, b)
	}
}
