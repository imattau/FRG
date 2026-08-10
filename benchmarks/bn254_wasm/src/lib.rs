#![no_std]

use ark_bn254::{Bn254, G1Affine, G2Affine};
use ark_ec::{pairing::Pairing, AffineRepr};
use core::alloc::{GlobalAlloc, Layout};
use core::panic::PanicInfo;
use core::ptr::{addr_of_mut, null_mut};
use core::sync::atomic::{AtomicUsize, Ordering};

struct BumpAlloc;

const HEAP_SIZE: usize = 64 * 1024 * 1024;
static mut HEAP: [u8; HEAP_SIZE] = [0; HEAP_SIZE];
static NEXT: AtomicUsize = AtomicUsize::new(0);

unsafe impl GlobalAlloc for BumpAlloc {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        let align = layout.align();
        let size = layout.size();
        let mut current = NEXT.load(Ordering::Relaxed);
        loop {
            let aligned = (current + align - 1) & !(align - 1);
            let next = match aligned.checked_add(size) {
                Some(next) if next <= HEAP_SIZE => next,
                _ => return null_mut(),
            };
            match NEXT.compare_exchange(current, next, Ordering::SeqCst, Ordering::Relaxed) {
                Ok(_) => return (addr_of_mut!(HEAP) as *mut u8).add(aligned),
                Err(observed) => current = observed,
            }
        }
    }

    unsafe fn dealloc(&self, _ptr: *mut u8, _layout: Layout) {}
}

#[global_allocator]
static ALLOC: BumpAlloc = BumpAlloc;

static mut SINK: u64 = 0;

#[no_mangle]
pub extern "C" fn init() {
    NEXT.store(0, Ordering::SeqCst);
}

#[no_mangle]
pub extern "C" fn call() -> i32 {
    let out = Bn254::pairing(G1Affine::generator(), G2Affine::generator());
    let text_len = format_len(&out);
    unsafe {
        SINK = SINK.wrapping_add(text_len as u64);
    }
    text_len as i32
}

fn format_len<T: core::fmt::Debug>(value: &T) -> usize {
    struct Counter(usize);
    impl core::fmt::Write for Counter {
        fn write_str(&mut self, s: &str) -> core::fmt::Result {
            self.0 += s.len();
            Ok(())
        }
    }
    let mut counter = Counter(0);
    let _ = core::fmt::write(&mut counter, format_args!("{:?}", value));
    counter.0
}

#[panic_handler]
fn panic(_info: &PanicInfo) -> ! {
    loop {}
}
