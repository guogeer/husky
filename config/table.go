package config

// 配置表格式仅支持制表符TAB分隔的表格
// 表格第一行为字段解释
// 表格第二行为字段KEY
// 表格第一列默认索引
// Version 2.0.0 支持隐藏属性，属性格式：".Key"，其中".Private"私有属性
// Version 2.1.0 列索引忽略大小写
// Version 2.2.0 表格名索引忽略大小写

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"github.com/guogeer/husky/log"
	"github.com/guogeer/husky/util"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tableFileSuffix = ".tbl"
	attrTable       = "system_table_field"
)

var (
	enableDebug = false
	gTableFiles sync.Map
	gTableRows  [255]*tableRow
)

func init() {
	for i := range gTableRows {
		gTableRows[i] = &tableRow{n: i}
	}
}

type tableFile struct {
	rowName map[string]int // 行名
	cells   []map[string]string
	name    string

	groups map[string]*tableGroup
}

type tableRow struct {
	n int
}

func newTableFile(name string) *tableFile {
	name = strings.ToLower(name)
	return &tableFile{
		name:    name,
		rowName: make(map[string]int),
	}
}

func (f *tableFile) Load(buf []byte) error {
	// f.rowName = make(map[string]int)
	s := string(buf) + "\n"
	s = strings.ReplaceAll(s, "\r\n", "\n")

	var colKeys []string
	for rowID := 0; len(s) > 0; rowID++ {
		line := s
		if n := strings.Index(s, "\n"); n >= 0 {
			line = s[:n]
		}
		s = s[len(line)+1:]
		// 忽略空字符串
		if b, _ := regexp.MatchString(`\S`, line); !b {
			// log.Warnf("config %s:%d empty", f.name, rowID)
			continue
		}

		lineCells := strings.Split(line, "\t")
		switch rowID {
		case 0: // 表格第一行为标题注释，忽略
		case 1: // 表格第二行列索引
			colKeys = lineCells
		default:
			rowName := string(lineCells[0])
			f.rowName[rowName] = rowID - 2

			cells := make(map[string]string)
			for k, cell := range lineCells {
				colKey := strings.ToLower(string(colKeys[k]))
				if colKey == ".private" && len(cell) > 0 {
					attrs := make(map[string]json.RawMessage)
					json.Unmarshal([]byte(cell), &attrs)
					for attrk, attrv := range attrs {
						attrk = strings.ToLower(attrk)
						s := string(attrv)
						// 格式"message"移除前缀后缀
						if ok, _ := regexp.MatchString(`^".*"$`, s); ok {
							s = s[1 : len(s)-1]
						}
						cells[attrk] = s
					}
				}
				cells[colKey] = string(cell)
			}
			f.cells = append(f.cells, cells)
		}
	}
	// 列索引忽略大小写
	return nil
}

func (f *tableFile) String(row, col interface{}) (string, bool) {
	if f == nil || row == nil || col == nil {
		return "", false
	}
	colKey := fmt.Sprintf("%v", col)
	colKey = strings.ToLower(colKey)

	rowN := -1
	if r, ok := row.(*tableRow); ok {
		rowN = r.n
	} else {
		rowKey := fmt.Sprintf("%v", row)
		if n, ok := f.rowName[rowKey]; ok {
			rowN = n
		}
	}
	if rowN >= 0 && rowN < len(f.cells) {
		s, ok := f.cells[rowN][colKey]
		return s, ok
	}
	return "", false
}

func (f *tableFile) Rows() []*tableRow {
	if f == nil {
		return nil
	}
	if len(f.cells) < len(gTableRows) {
		return gTableRows[:len(f.cells)]
	}

	rows := make([]*tableRow, 0, 32)
	for k := range f.cells {
		rows = append(rows, &tableRow{n: k})
	}
	return rows
}

type tableGroup struct {
	members []string
}

func newTableGroup(name string) *tableGroup {
	name = strings.ToLower(name)
	return &tableGroup{members: []string{name}}
}

func getTableGroup(name string) *tableGroup {
	name = strings.ToLower(name)
	if f := getTableFile(attrTable); f != nil {
		if group, ok := f.groups[name]; ok {
			return group
		}
	}
	return newTableGroup(name)
}

func (g *tableGroup) String(row, col interface{}) (string, bool) {
	for _, name := range g.members {
		if s, ok := getTableFile(name).String(row, col); ok {
			return s, ok
		}
	}
	return "", false
}

func scanOne(val reflect.Value, s string) {
	if s == "" {
		return
	}
	switch util.ConvertKind(val.Kind()) {
	default:
		panic("unsupport type" + val.Type().String())
	case reflect.Ptr:
		scanOne(val.Elem(), s)
	case reflect.Int64:
		n, _ := strconv.ParseInt(s, 10, 64)
		val.SetInt(n)
	case reflect.Uint64:
		n, _ := strconv.ParseUint(s, 10, 64)
		val.SetUint(n)
	case reflect.Float64:
		a, _ := strconv.ParseFloat(s, 64)
		val.SetFloat(a)
	case reflect.Bool:
		b, _ := strconv.ParseBool(s)
		val.SetBool(b)
	case reflect.String:
		val.SetString(s)
	case reflect.Slice:
		ss := util.ParseStrings(s)
		newval := reflect.MakeSlice(val.Type(), len(ss), len(ss))
		for i, s2 := range ss {
			scanOne(newval.Index(i), s2)
		}
		val.Set(newval)
	}
}

func (g *tableGroup) Scan(row, cols interface{}, args []interface{}) (int, error) {
	s := fmt.Sprintf("%v", cols)
	colKeys := strings.Split(s, ",")
	if len(colKeys) != len(args) {
		panic("args not match")
	}
	for i, arg := range args {
		colKey := strings.ToLower(colKeys[i])
		s, _ := g.String(row, colKey)

		switch arg.(type) {
		case *time.Duration:
			arg = (*durationCell)(arg.(*time.Duration))
		case *time.Time:
			arg = (*timeCell)(arg.(*time.Time))
		}
		if scanner, ok := arg.(Scanner); ok {
			scanner.Scan(s)
		} else {
			scanOne(reflect.ValueOf(arg), s)
		}
	}
	return 0, nil
}

func readFile(path string, rc io.ReadCloser) {
	if rc == nil {
		return
	}
	defer rc.Close()

	ext := filepath.Ext(path)
	if ext != tableFileSuffix {
		return
	}

	buf, err := ioutil.ReadAll(rc)
	if err != nil {
		log.Fatal(err)
	}
	base := filepath.Base(path)
	name := base[:len(base)-len(ext)]
	if err := LoadTable(name, buf); err != nil {
		log.Fatal(err)
	}
}

// 加载tables下所有的tbl文件
func LoadLocalTables(fileName string) {
	// 第一步加载tables.zip
	zipFile := fileName + ".zip"
	if _, err := os.Stat(zipFile); err == nil {
		if enableDebug {
			log.Infof("load tables %s", zipFile)
		}
		r, err := zip.OpenReader(fileName + ".zip")
		if err != nil {
			panic(err)
		}
		defer r.Close()

		for _, f := range r.File {
			rc, err := f.Open()
			if err != nil {
				log.Fatal(err)
			}
			readFile(f.Name, rc)
		}
	}
	// 第二部加载scripts/*.tbl
	fileInfo, err := os.Stat(fileName)
	if err == nil && fileInfo.IsDir() {
		if enableDebug {
			log.Infof("load tables %s/*", fileName)
		}
		files, err := ioutil.ReadDir(fileName)
		if err != nil {
			log.Fatal(err)
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			path := fileName + "/" + f.Name()
			rc, err := os.Open(path)
			if err != nil {
				log.Fatal(err)
			}
			readFile(path, rc)
		}
	}
}

func getTableFile(name string) *tableFile {
	name = strings.ToLower(name)
	if f, ok := gTableFiles.Load(name); ok {
		return f.(*tableFile)
	}
	return nil
}

func Rows(name string) []*tableRow {
	return getTableFile(name).Rows()
}

func String(name string, row, col interface{}, def ...string) (string, bool) {
	return getTableGroup(name).String(row, col)
}

func Int(name string, row, col interface{}, def ...int64) (int64, bool) {
	s, ok := getTableGroup(name).String(row, col)
	if ok == false {
		for _, n := range def {
			return n, false
		}
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		log.Errorf("cell %v:%v:%v[%v] invalid %v", name, row, col, s, err)
	}
	return n, ok
}

func Float(name string, row, col interface{}, def ...float64) (float64, bool) {
	s, ok := getTableGroup(name).String(row, col)
	if ok == false {
		for _, a := range def {
			return a, false
		}
		return 0.0, false
	}
	a, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Errorf("cellfloat %v:%v:%v[%v] invalid %v", name, row, col, s, err)
	}
	return a, ok
}

func Time(name string, row, col interface{}) (time.Time, bool) {
	if s, ok := getTableGroup(name).String(row, col); ok {
		if t, err := util.ParseTime(s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// 默认单位秒
// 120s、120m、120h、120d，分别表示秒，分，时，天
func Duration(name string, row, col interface{}, def ...time.Duration) (time.Duration, bool) {
	if s, ok := getTableGroup(name).String(row, col); ok && len(s) > 0 {
		d, _ := parseDuration(s)
		return d, true
	}
	for _, d := range def {
		return d, false
	}
	return 0, false
}

func Scan(name string, row, colArgs interface{}, args ...interface{}) (int, error) {
	return getTableGroup(name).Scan(row, colArgs, args)
}

func Row(name string) int {
	f := getTableFile(name)
	if f == nil {
		return -1
	}
	return len(f.cells)
}

func RowId(n int) *tableRow {
	if n >= 0 && n < len(gTableRows) {
		return gTableRows[n]
	}
	return &tableRow{n: n}
}

// TODO 当前仅支持,分隔符
func IsPart(s string, match interface{}) bool {
	smatch := fmt.Sprintf("%v", match)
	return strings.Contains(","+s+",", ","+smatch+",")
}

func LoadTable(name string, buf []byte) error {
	name = strings.ToLower(name)
	t := newTableFile(name)
	if enableDebug {
		log.Infof("load table %s", name)
	}
	if err := t.Load(buf); err != nil {
		return err
	}
	if name == attrTable {
		t.groups = make(map[string]*tableGroup)
		for _, row := range t.Rows() {
			s, _ := t.String(row, "Field")
			path := strings.Split(s, ".")
			if len(path) > 1 && path[1] == "*" {
				gname, ok := t.String(row, "Group")
				if ok && gname != "" {
					gname = strings.ToLower(gname)
					if _, ok := t.groups[gname]; !ok {
						t.groups[gname] = newTableGroup(gname)
					}
					g := t.groups[gname]
					g.members = append(g.members, path[0])
				}
			}
		}
	}
	gTableFiles.Store(name, t)
	return nil
}
