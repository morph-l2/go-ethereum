#![feature(once_cell)]

pub mod checker {
    use crate::utils::{c_char_to_vec, bool_to_int};
    use libc::c_char;
    use std::cell::OnceCell;
    use std::panic;
    use types::eth::BlockTrace;
    use zkevm::{capacity_checker::CircuitCapacityChecker};

    static mut CHECKER: OnceCell<CircuitCapacityChecker> = OnceCell::new();

    /// # Safety
    #[no_mangle]
    pub unsafe extern "C" fn new_circuit_capacity_checker() {
        env_logger::Builder::from_env(env_logger::Env::default().default_filter_or("debug"))
            .format_timestamp_millis()
            .init();

        let c = CircuitCapacityChecker::new();
        CHECKER.set(c).unwrap();
    }

    /// # Safety
    #[no_mangle]
    pub unsafe extern "C" fn reset_circuit_capacity_checker() {
        CHECKER.get_mut().unwrap().reset()
    }

    /// # Safety
    #[no_mangle]
    pub unsafe extern "C" fn apply_tx(tx_traces: *const c_char) -> c_char {
        let tx_traces_vec = c_char_to_vec(tx_traces);
        let traces = serde_json::from_slice::<BlockTrace>(&tx_traces_vec).unwrap();
        let ok = panic::catch_unwind(|| {
            CHECKER.get_mut().unwrap()
                .estimate_circuit_capacity(&[traces]).is_ok()
        });
        ok.unwrap_or(false) as c_char
    }
}

pub(crate) mod utils {
    use std::ffi::{CStr, CString};
    use std::os::raw::c_char;

    pub(crate) fn c_char_to_str(c: *const c_char) -> &'static str {
        let cstr = unsafe { CStr::from_ptr(c) };
        cstr.to_str().unwrap()
    }

    pub(crate) fn c_char_to_vec(c: *const c_char) -> Vec<u8> {
        let cstr = unsafe { CStr::from_ptr(c) };
        cstr.to_bytes().to_vec()
    }

    pub(crate) fn vec_to_c_char(bytes: Vec<u8>) -> *const c_char {
        CString::new(bytes).unwrap().into_raw()
    }

    pub(crate) fn bool_to_int(b: bool) -> u8 {
        match b {
            true => 1,
            false => 0,
        }
    }
}
