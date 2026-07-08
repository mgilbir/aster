use std::alloc::{alloc, dealloc as std_dealloc, Layout};
use std::cell::RefCell;
use std::slice;
use std::sync::Arc;

use resvg::tiny_skia;
use resvg::usvg;

// The WASM module is single-threaded (WASI reactor, no threads), so
// thread-local interior mutability gives sound, panic-free access to the shared
// state without `static mut` (which is UB-adjacent and requires unsafe on every
// touch). The font database is kept behind an Arc so each render can hand usvg
// a cheap clone (a refcount bump) rather than copying the whole font set.
thread_local! {
    static FONT_DB: RefCell<Option<Arc<usvg::fontdb::Database>>> = const { RefCell::new(None) };
    static RESULT_BUF: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
    static ERROR_BUF: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
}

#[no_mangle]
pub extern "C" fn alloc_mem(size: u32) -> u32 {
    let layout = Layout::from_size_align(size as usize, 1).unwrap();
    unsafe { alloc(layout) as u32 }
}

#[no_mangle]
pub extern "C" fn dealloc_mem(ptr: u32, size: u32) {
    let layout = Layout::from_size_align(size as usize, 1).unwrap();
    unsafe { std_dealloc(ptr as *mut u8, layout) }
}

#[no_mangle]
pub extern "C" fn font_db_init() {
    FONT_DB.with(|db| *db.borrow_mut() = Some(Arc::new(usvg::fontdb::Database::new())));
}

#[no_mangle]
pub extern "C" fn font_db_set_sans_serif(ptr: u32, len: u32) -> i32 {
    let data = unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) };
    let name = match std::str::from_utf8(data) {
        Ok(s) => s.to_string(),
        Err(e) => {
            set_error(&format!("invalid UTF-8: {}", e));
            return -1;
        }
    };
    with_font_db_mut(|db| db.set_sans_serif_family(name))
}

#[no_mangle]
pub extern "C" fn font_db_set_serif(ptr: u32, len: u32) -> i32 {
    let data = unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) };
    let name = match std::str::from_utf8(data) {
        Ok(s) => s.to_string(),
        Err(e) => {
            set_error(&format!("invalid UTF-8: {}", e));
            return -1;
        }
    };
    with_font_db_mut(|db| db.set_serif_family(name))
}

#[no_mangle]
pub extern "C" fn font_db_set_monospace(ptr: u32, len: u32) -> i32 {
    let data = unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) };
    let name = match std::str::from_utf8(data) {
        Ok(s) => s.to_string(),
        Err(e) => {
            set_error(&format!("invalid UTF-8: {}", e));
            return -1;
        }
    };
    with_font_db_mut(|db| db.set_monospace_family(name))
}

#[no_mangle]
pub extern "C" fn font_db_set_cursive(ptr: u32, len: u32) -> i32 {
    let data = unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) };
    let name = match std::str::from_utf8(data) {
        Ok(s) => s.to_string(),
        Err(e) => {
            set_error(&format!("invalid UTF-8: {}", e));
            return -1;
        }
    };
    with_font_db_mut(|db| db.set_cursive_family(name))
}

#[no_mangle]
pub extern "C" fn font_db_set_fantasy(ptr: u32, len: u32) -> i32 {
    let data = unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) };
    let name = match std::str::from_utf8(data) {
        Ok(s) => s.to_string(),
        Err(e) => {
            set_error(&format!("invalid UTF-8: {}", e));
            return -1;
        }
    };
    with_font_db_mut(|db| db.set_fantasy_family(name))
}

#[no_mangle]
pub extern "C" fn font_db_add(ptr: u32, len: u32) -> i32 {
    let data = unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) };
    let owned = data.to_vec();
    with_font_db_mut(|db| db.load_font_data(owned))
}

// with_font_db_mut runs f against the mutable font database, returning 0 on
// success or -1 (with an error message set) when the database is uninitialized
// or unexpectedly shared. Fonts are loaded once at startup while no render
// holds a clone, so the Arc is uniquely owned here; the shared case is handled
// gracefully instead of panicking (the previous Arc::get_mut().unwrap()).
fn with_font_db_mut<F: FnOnce(&mut usvg::fontdb::Database)>(f: F) -> i32 {
    FONT_DB.with(|cell| match cell.borrow_mut().as_mut() {
        Some(arc) => match Arc::get_mut(arc) {
            Some(db) => {
                f(db);
                0
            }
            None => {
                set_error("font_db is in use and cannot be modified");
                -1
            }
        },
        None => {
            set_error("font_db not initialized");
            -1
        }
    })
}

#[no_mangle]
pub extern "C" fn render(svg_ptr: u32, svg_len: u32, scale_bits: u64) -> i32 {
    RESULT_BUF.with(|b| b.borrow_mut().clear());
    ERROR_BUF.with(|b| b.borrow_mut().clear());

    let scale = f64::from_bits(scale_bits);

    let svg_data = unsafe { slice::from_raw_parts(svg_ptr as *const u8, svg_len as usize) };
    let svg_str = match std::str::from_utf8(svg_data) {
        Ok(s) => s,
        Err(e) => {
            set_error(&format!("invalid UTF-8: {}", e));
            return -1;
        }
    };

    let db = match FONT_DB.with(|c| c.borrow().clone()) {
        Some(db) => db,
        None => {
            set_error("font_db not initialized");
            return -1;
        }
    };

    let mut opts = usvg::Options::default();
    opts.fontdb = db;

    let tree = match usvg::Tree::from_str(svg_str, &opts) {
        Ok(t) => t,
        Err(e) => {
            set_error(&format!("SVG parse error: {}", e));
            return -1;
        }
    };

    let size = tree.size();
    let w = (size.width() as f64 * scale).ceil() as u32;
    let h = (size.height() as f64 * scale).ceil() as u32;

    if w == 0 || h == 0 {
        set_error("SVG has zero dimensions");
        return -1;
    }

    let mut pixmap = match tiny_skia::Pixmap::new(w, h) {
        Some(p) => p,
        None => {
            set_error("failed to create pixmap");
            return -1;
        }
    };

    let transform = tiny_skia::Transform::from_scale(scale as f32, scale as f32);
    resvg::render(&tree, transform, &mut pixmap.as_mut());

    let png_data = match pixmap.encode_png() {
        Ok(d) => d,
        Err(e) => {
            set_error(&format!("PNG encode error: {}", e));
            return -1;
        }
    };

    RESULT_BUF.with(|b| *b.borrow_mut() = png_data);
    0
}

#[no_mangle]
pub extern "C" fn result_ptr() -> u32 {
    RESULT_BUF.with(|b| b.borrow().as_ptr() as u32)
}

#[no_mangle]
pub extern "C" fn result_len() -> u32 {
    RESULT_BUF.with(|b| b.borrow().len() as u32)
}

#[no_mangle]
pub extern "C" fn error_ptr() -> u32 {
    ERROR_BUF.with(|b| b.borrow().as_ptr() as u32)
}

#[no_mangle]
pub extern "C" fn error_len() -> u32 {
    ERROR_BUF.with(|b| b.borrow().len() as u32)
}

fn set_error(msg: &str) {
    ERROR_BUF.with(|b| {
        let mut buf = b.borrow_mut();
        buf.clear();
        buf.extend_from_slice(msg.as_bytes());
    });
}
