import{c as r}from"./createLucideIcon-BFCOMs5c.js";import{E as f,G as k,o as s,e as p,c as l,a as o,t as h,q as v,u as w,P as c,d as i,p as B,w as V,T as M}from"./index-CjEFJfnL.js";/**
 * @license lucide-vue-next v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const C=r("Trash2Icon",[["path",{d:"M3 6h18",key:"d0wm0j"}],["path",{d:"M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6",key:"4alrt4"}],["path",{d:"M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2",key:"v07s0e"}],["line",{x1:"10",x2:"10",y1:"11",y2:"17",key:"1uufr5"}],["line",{x1:"14",x2:"14",y1:"11",y2:"17",key:"xtxkd"}]]);/**
 * @license lucide-vue-next v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const b=r("XIcon",[["path",{d:"M18 6 6 18",key:"1bl5f8"}],["path",{d:"m6 6 12 12",key:"d8bk6v"}]]),_={class:"modal-header"},x={class:"modal-title"},E={class:"modal-body"},T={key:0,class:"modal-footer"},I={__name:"Modal",props:{modelValue:Boolean,title:{type:String,default:""},size:{type:String,default:""},closeOnBackdrop:{type:Boolean,default:!0}},emits:["update:modelValue"],setup(t,{emit:m}){const d=t,u=m;function a(){u("update:modelValue",!1)}function y(){d.closeOnBackdrop&&a()}function n(e){e.key==="Escape"&&d.modelValue&&a()}return f(()=>window.addEventListener("keydown",n)),k(()=>window.removeEventListener("keydown",n)),(e,z)=>(s(),p(M,{to:"body"},[t.modelValue?(s(),l("div",{key:0,class:"modal-backdrop",onMousedown:V(y,["self"])},[o("div",{class:B(["modal",t.size])},[o("div",_,[o("div",x,h(t.title),1),o("button",{class:"icon-btn modal-close",onClick:a},[v(w(b),{size:17})])]),o("div",E,[c(e.$slots,"default")]),e.$slots.footer?(s(),l("div",T,[c(e.$slots,"footer")])):i("",!0)],2)],32)):i("",!0)]))}};export{C as T,b as X,I as _};
