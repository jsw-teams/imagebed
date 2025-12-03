const fileInput=document.getElementById("file-input");
const dropzone=document.getElementById("dropzone");
const btnChoose=document.getElementById("btn-choose");
const btnUpload=document.getElementById("btn-upload");
const fileNameEl=document.getElementById("file-name");
const statusText=document.getElementById("status-text");
const resultBox=document.getElementById("result-box");
const resultUrl=document.getElementById("result-url");
const btnCopy=document.getElementById("btn-copy");

function setStatus(msg){statusText.textContent=msg||"";}

function handleFiles(files){
  if(!files||!files.length)return;
  const f=files[0];
  fileInput.files=files;
  fileNameEl.textContent=f.name+" · "+Math.round(f.size/1024)+" KB";
  resultBox.style.display="none";
  setStatus("");
}

btnChoose.addEventListener("click",()=>fileInput.click());
fileInput.addEventListener("change",e=>handleFiles(e.target.files));

dropzone.addEventListener("dragover",e=>{
  e.preventDefault();
  dropzone.style.borderColor="#4f46e5";
  dropzone.style.background="#eef2ff";
});
dropzone.addEventListener("dragleave",e=>{
  e.preventDefault();
  dropzone.style.borderColor="rgba(148,163,184,0.9)";
  dropzone.style.background="rgba(255,255,255,0.95)";
});
dropzone.addEventListener("drop",e=>{
  e.preventDefault();
  dropzone.style.borderColor="rgba(148,163,184,0.9)";
  dropzone.style.background="rgba(255,255,255,0.95)";
  handleFiles(e.dataTransfer.files);
});

btnUpload.addEventListener("click",async()=>{
  const files=fileInput.files;
  if(!files||!files.length){
    setStatus("请先选择一张图片。");
    return;
  }
  const form=new FormData();
  form.append("file",files[0]);

  btnUpload.disabled=true;
  setStatus("正在上传，请稍候…");

  try{
    const resp=await fetch("/api/upload",{method:"POST",body:form});
    const data=await resp.json().catch(()=>({}));
    if(!resp.ok)throw new Error(data.error||resp.statusText||"上传失败");
    const id=data.id||data.image_id||data.imageID;
    if(!id)throw new Error("后端没有返回图片 ID。");
    const url=`${location.protocol}//${location.host}/i/${id}`;
    resultUrl.value=url;
    resultBox.style.display="block";
    setStatus("上传成功！");
  }catch(err){
    resultBox.style.display="none";
    setStatus("上传失败："+(err&&err.message?err.message:String(err)));
  }finally{
    btnUpload.disabled=false;
  }
});

btnCopy.addEventListener("click",async()=>{
  const text=resultUrl.value;
  if(!text)return;
  try{
    await navigator.clipboard.writeText(text);
    setStatus("链接已复制到剪贴板。");
  }catch(e){
    try{
      resultUrl.select();
      document.execCommand("copy");
      setStatus("链接已复制到剪贴板。");
    }catch{
      setStatus("复制失败，请手动复制链接。");
    }
  }
});