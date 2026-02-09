use std::alloc::{alloc, dealloc as std_dealloc, Layout};
use std::slice;
use std::sync::Arc;

use resvg::tiny_skia;
use resvg::usvg;

static mut FONT_DB: Option<Arc<usvg::fontdb::Database>> = None;
static mut RESULT_BUF: Vec<u8> = Vec::new();
static mut ERROR_BUF: Vec<u8> = Vec::new();

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
    unsafe {
        FONT_DB = Some(Arc::new(usvg::fontdb::Database::new()));
    }
}

#[no_mangle]
pub extern "C" fn font_db_set_sans_serif(ptr: u32, len: u32) -> i32 {
    unsafe {
        let data = slice::from_raw_parts(ptr as *const u8, len as usize);
        let name = match std::str::from_utf8(data) {
            Ok(s) => s,
            Err(e) => {
                set_error(&format!("invalid UTF-8: {}", e));
                return -1;
            }
        };
        if let Some(ref mut db) = FONT_DB {
            Arc::get_mut(db).unwrap().set_sans_serif_family(name);
            0
        } else {
            set_error("font_db not initialized");
            -1
        }
    }
}

#[no_mangle]
pub extern "C" fn font_db_set_monospace(ptr: u32, len: u32) -> i32 {
    unsafe {
        let data = slice::from_raw_parts(ptr as *const u8, len as usize);
        let name = match std::str::from_utf8(data) {
            Ok(s) => s,
            Err(e) => {
                set_error(&format!("invalid UTF-8: {}", e));
                return -1;
            }
        };
        if let Some(ref mut db) = FONT_DB {
            Arc::get_mut(db).unwrap().set_monospace_family(name);
            0
        } else {
            set_error("font_db not initialized");
            -1
        }
    }
}

#[no_mangle]
pub extern "C" fn font_db_add(ptr: u32, len: u32) -> i32 {
    unsafe {
        let data = slice::from_raw_parts(ptr as *const u8, len as usize);
        if let Some(ref mut db) = FONT_DB {
            Arc::get_mut(db).unwrap().load_font_data(data.to_vec());
            0
        } else {
            set_error("font_db not initialized");
            -1
        }
    }
}

#[no_mangle]
pub extern "C" fn render(svg_ptr: u32, svg_len: u32, scale_bits: u64) -> i32 {
    unsafe {
        RESULT_BUF.clear();
        ERROR_BUF.clear();
    }

    let scale = f64::from_bits(scale_bits);

    let svg_data = unsafe { slice::from_raw_parts(svg_ptr as *const u8, svg_len as usize) };
    let svg_str = match std::str::from_utf8(svg_data) {
        Ok(s) => s,
        Err(e) => {
            set_error(&format!("invalid UTF-8: {}", e));
            return -1;
        }
    };

    let db = unsafe {
        match FONT_DB.as_ref() {
            Some(db) => db.clone(),
            None => {
                set_error("font_db not initialized");
                return -1;
            }
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

    unsafe {
        RESULT_BUF = png_data;
    }
    0
}

#[no_mangle]
pub extern "C" fn result_ptr() -> u32 {
    unsafe { RESULT_BUF.as_ptr() as u32 }
}

#[no_mangle]
pub extern "C" fn result_len() -> u32 {
    unsafe { RESULT_BUF.len() as u32 }
}

#[no_mangle]
pub extern "C" fn error_ptr() -> u32 {
    unsafe { ERROR_BUF.as_ptr() as u32 }
}

#[no_mangle]
pub extern "C" fn error_len() -> u32 {
    unsafe { ERROR_BUF.len() as u32 }
}

fn set_error(msg: &str) {
    unsafe {
        ERROR_BUF = msg.as_bytes().to_vec();
    }
}
