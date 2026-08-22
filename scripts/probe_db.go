// 临时 probe：读取 filePathDb.sqlite，打印前 30 条记录
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := `c:\Users\liwl\Downloads\AI\faststrm-go\dist\faststrm_windows_amd64\data\filePathDb.sqlite`
	if len(os.Args) > 1 {
		dbPath = os.Args[1]
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// 1) 统计表&账号数
	row := db.QueryRowContext(context.Background(), "SELECT COUNT(*), COUNT(DISTINCT account) FROM files")
	var cnt, accs int
	if err := row.Scan(&cnt, &accs); err != nil {
		panic(err)
	}
	fmt.Printf("=== DB snapshot: file entries=%d accounts=%d ===\n", cnt, accs)

	// 2) 按「路径段数」统计：单段（无/） vs 多段
	q := `
SELECT
  CASE WHEN INSTR(path,'/')>0 THEN 'MULTI_SEG' ELSE 'SINGLE_SEG' END AS seg,
  COUNT(*) AS n
FROM files
GROUP BY seg
`
	rows, err := db.QueryContext(context.Background(), q)
	if err != nil {
		panic(err)
	}
	for rows.Next() {
		var seg string
		var n int
		_ = rows.Scan(&seg, &n)
		fmt.Printf("  path segment: %-10s count=%d\n", seg, n)
	}
	rows.Close()

	// 3) 展示具体记录：前 40 条
	fmt.Println("\n=== Top 40 entries ===")
	rows2, err := db.QueryContext(context.Background(), `SELECT account, file_id, path, file_name, parent_id, pickcode FROM files ORDER BY update_time DESC LIMIT 40`)
	if err != nil {
		panic(err)
	}
	defer rows2.Close()
	i := 0
	for rows2.Next() {
		i++
		var acc, fid, path, fname, pid, pick sql.NullString
		_ = rows2.Scan(&acc, &fid, &path, &fname, &pid, &pick)
		fmt.Printf("%2d | acc=%-10s fid=%-22s pid=%-3s path=%-60s name=%s\n",
			i,
			trunc(acc.String, 10), trunc(fid.String, 22), trunc(pid.String, 3),
			trunc(path.String, 60), fname.String)
	}
	fmt.Println("\nDONE.")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
