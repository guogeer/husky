package util

import (
	// "fmt"
	"reflect"
)

type deepCopyer interface {
	DeepCopy(src interface{}) bool
}

// 深拷贝
// 结构体、切片之间递归深拷贝
// 整数、浮点数、字符串、布尔类型直接拷贝，其他类型忽略
// 增加tag alias
// 2020-04-29 结构体匿名字段可拷贝
// 2020-07-16 修复源数据字段拷贝时跳过
func DeepCopy(dst, src interface{}) {
	if dst != nil && src != nil {
		sval := reflect.ValueOf(src)
		dval := reflect.ValueOf(dst)
		doCopy(dval, sval)
	}
}

func doCopy(dval, sval reflect.Value) {
	if dval.Kind() == reflect.Ptr && dval.CanSet() && dval.IsZero() && !sval.IsZero() {
		dval.Set(reflect.New(dval.Type().Elem()))
	}

	if dval.IsValid() && dval.CanInterface() {
		// fmt.Println(dval.Interface())
		if cp, ok := dval.Interface().(deepCopyer); ok && cp.DeepCopy(sval.Interface()) {
			return
		}
	}

	sval = reflect.Indirect(sval)
	dval = reflect.Indirect(dval)
	if ConvertKind(sval.Kind()) != ConvertKind(dval.Kind()) {
		return
	}
	if !dval.CanSet() {
		return
	}

	// fmt.Println(sval.IsValid(), dval.CanSet())
	switch ConvertKind(sval.Kind()) {
	case reflect.Int64:
		dval.SetInt(sval.Int())
	case reflect.Uint64:
		dval.SetUint(sval.Uint())
	case reflect.Float64:
		dval.SetFloat(sval.Float())
	case reflect.Bool:
		dval.SetBool(sval.Bool())
	case reflect.String:
		dval.SetString(sval.String())
	case reflect.Struct:
		aliasFields := map[string]int{}
		for i := 0; i < dval.NumField(); i++ {
			alias := dval.Type().Field(i).Tag.Get("alias")
			if alias != "" {
				aliasFields[alias] = i
			}
		}

		for i := 0; i < sval.NumField(); i++ {
			sfield := sval.Field(i)
			sname := sval.Type().Field(i).Name
			stype := sval.Type().Field(i)
			if tag := stype.Tag.Get("alias"); tag != "" {
				sname = tag
			}
			dfield := dval.FieldByName(sname)
			if aliasIndex, ok := aliasFields[sname]; ok {
				dfield = dval.Field(aliasIndex)
			}
			// fmt.Println("##", dfield.Kind(), dfield.CanSet(), dval.Kind())
			// fmt.Println("==", sname, sfield.Kind(), sfield.CanSet(), stype)
			// exported anonymous struct field
			doCopy(dfield, sfield)
			if stype.PkgPath == "" && stype.Anonymous {
				doCopy(dval, sfield)
			}
		}
	case reflect.Slice, reflect.Array:
		if size := sval.Len(); size > 0 && sval.Kind() == reflect.Slice {
			newval := reflect.MakeSlice(dval.Type(), size, size)
			dval.Set(newval)
		}
		for i := 0; i < sval.Len() && i < dval.Len(); i++ {
			v1, v2 := dval.Index(i), sval.Index(i)
			doCopy(v1, v2)
		}
	}
}

func ConvertKind(k reflect.Kind) reflect.Kind {
	switch k {
	case reflect.Float32, reflect.Float64:
		return reflect.Float64
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Int:
		return reflect.Int64
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uint:
		return reflect.Uint64
	}
	return k
}
