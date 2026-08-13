// gcm-migrate: cmx.db → gcm 库迁移 CLI。
//
//	用法: gcm-migrate <cmx.db> <gcm.db> [-types types.yaml] [-category-type category]
//	      [-category-field categories] [-parent-field parent]
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/kran/gcm/migrate"
)

func main() {
	typesPath := flag.String("types", "types.yaml", "gcm types.yaml（字段过滤/校验）")
	catType := flag.String("category-type", "category", "分类类型名")
	catField := flag.String("category-field", "categories", "内容挂载分类的 ref 字段名")
	parentField := flag.String("parent-field", "parent", "分类父引用 ref 字段名")
	flag.Parse()
	if flag.NArg() != 2 {
		log.Fatal("usage: gcm-migrate <cmx.db> <gcm.db> [-types types.yaml]")
	}
	var defs []byte
	if *typesPath != "" {
		var err error
		defs, err = os.ReadFile(*typesPath)
		if err != nil {
			log.Fatalf("read types: %v", err)
		}
	}
	st, err := migrate.Migrate(flag.Arg(0), flag.Arg(1), migrate.Options{
		TypeDefs: defs, CategoryType: *catType, CategoryField: *catField, ParentField: *parentField,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("迁移完成: %d 分类, %d 内容, %d 引用\n", st.Categories, st.Nodes, st.Edges)
}
