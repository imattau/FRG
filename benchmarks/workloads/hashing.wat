(module
  (import "frg" "log" (func $log (param i32 i32)))
  (memory (export "memory") 1)
  (func (export "init"))
  (func (export "call")
    (local $i i32)
    (loop $loop
      (call $log (i32.const 0) (i32.const 4))
      (i32.add (local.get $i) (i32.const 1))
      (local.tee $i)
      (i32.const 10000)
      (i32.lt_u)
      (br_if $loop)))
)
