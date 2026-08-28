package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "dist/faststrm_windows_amd64/data/filePathDb.sqlite")
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer db.Close()

	// 查 files 表结构
	rows, _ := db.Query("PRAGMA table_info(files)")
	if rows != nil {
		fmt.Println("=== files columns ===")
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt sql.NullString
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			fmt.Printf("  %s %s (notnull=%d pk=%d)\n", name, typ, notnull, pk)
		}
		rows.Close()
	}

	// 查 files 表内容
	rows, err = db.Query("SELECT * FROM files LIMIT 20")
	if err != nil {
		fmt.Println("query:", err)
		return
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	fmt.Println("\n=== files data ===")
	fmt.Println("columns:", cols)
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rows.Scan(ptrs...)
		for i, v := range vals {
			if v.Valid {
				fmt.Printf("  %s=%s", cols[i], v.String)
			}
		}
		fmt.Println()
	}
}
